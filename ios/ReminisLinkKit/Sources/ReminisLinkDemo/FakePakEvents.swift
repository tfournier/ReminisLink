// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import Foundation
import ReminisLinkKit

// Milestone M14 of the fake pak: `GET /events`, with the rules of the
// contract: `hello` first, `: keep-alive` (injectable interval), `library`
// after every change of the card made through the API (any client), `job`
// for every job change (jobs advance by themselves while a stream is
// open), `device` when the battery, the connected clients or the free
// space change, `shutdown` when the pak quits, 4 streams per client and 16
// in all (`429`), and a mode without the route (a pak older than M14).

/// The open streams and their timers.
final class FakeEventHub: @unchecked Sendable {
    struct Subscriber {
        let clientId: String
        let stream: FakeStream
        var nextId = 1
    }

    struct DeviceSnapshot: Equatable {
        let battery: Battery
        let clients: [String]
        let freeMiB: Int64
    }

    struct Battery: Equatable {
        var percent: Int
        var charging: Bool
    }

    let lock = NSLock()
    var subscribers: [ObjectIdentifier: Subscriber] = [:]
    var supported = true
    var keepAliveInterval: TimeInterval = 15
    var keepAlivesPaused = false
    var jobTickInterval: TimeInterval? = 1
    var retryAfter = 5
    var battery: Battery? = Battery(percent: 78, charging: false)
    var lastDevice: DeviceSnapshot?
    /// Cadences of the contract: `library` at most once a second (changes
    /// merged), `device` at most once every 5 s.
    var libraryInterval: TimeInterval = 1
    var deviceInterval: TimeInterval = 5
    var lastLibraryEmit: Date?
    var lastDeviceEmit: Date?
    /// Systems waiting for the next `library` event (nil inside: unknown,
    /// everything), and whether a send is scheduled.
    var pendingLibrary: Set<String>??
    var libraryScheduled = false
    var devicePending = false
    var deviceScheduled = false
    var keepAliveTimer: DispatchSourceTimer?
    var jobTimer: DispatchSourceTimer?
    let queue = DispatchQueue(label: "FakePak.events")

    var streamingClients: [String] {
        lock.withLock { subscribers.values.map(\.clientId) }
    }
}

extension FakePak {
    public static let maxEventStreamsPerClient = 4
    public static let maxEventStreams = 16

    // MARK: - Controls

    /// `GET /events` exists (false: a pak older than M14, `404`).
    public var eventsSupported: Bool {
        get { eventHub.lock.withLock { eventHub.supported } }
        set { eventHub.lock.withLock { eventHub.supported = newValue } }
    }

    /// Seconds between two `: keep-alive` (15 in the contract).
    public var keepAliveInterval: TimeInterval {
        get { eventHub.lock.withLock { eventHub.keepAliveInterval } }
        set {
            eventHub.lock.withLock { eventHub.keepAliveInterval = newValue }
            restartEventTimers()
        }
    }

    /// No more keep-alives (as a console gone to sleep): the app sees no
    /// byte at all.
    public var keepAlivesPaused: Bool {
        get { eventHub.lock.withLock { eventHub.keepAlivesPaused } }
        set { eventHub.lock.withLock { eventHub.keepAlivesPaused = newValue } }
    }

    /// While a stream is open, unfinished jobs advance one step every this
    /// many seconds (as they would on the console) and their changes are
    /// sent; nil: they advance only when polled.
    public var jobTickInterval: TimeInterval? {
        get { eventHub.lock.withLock { eventHub.jobTickInterval } }
        set {
            eventHub.lock.withLock { eventHub.jobTickInterval = newValue }
            restartEventTimers()
        }
    }

    /// `Retry-After` of the `429` beyond the stream limits.
    public var eventsRetryAfter: Int {
        get { eventHub.lock.withLock { eventHub.retryAfter } }
        set { eventHub.lock.withLock { eventHub.retryAfter = newValue } }
    }

    /// The shortest gap between two `library` events (1 s in the contract).
    public var libraryEventInterval: TimeInterval {
        get { eventHub.lock.withLock { eventHub.libraryInterval } }
        set { eventHub.lock.withLock { eventHub.libraryInterval = newValue } }
    }

    /// The shortest gap between two `device` events (5 s in the contract).
    public var deviceEventInterval: TimeInterval {
        get { eventHub.lock.withLock { eventHub.deviceInterval } }
        set { eventHub.lock.withLock { eventHub.deviceInterval = newValue } }
    }

    /// Sends now what the cadences hold back, as if their interval had
    /// passed (tests set long intervals and call this: no timing).
    public func flushThrottledEvents() {
        flushLibrary()
        flushDevice()
    }

    /// Whether a `library` event waits for its interval.
    public var libraryEventPending: Bool {
        eventHub.lock.withLock { eventHub.pendingLibrary != nil }
    }

    /// Open streams, of every client or of one.
    public func eventStreamCount(clientId: String? = nil) -> Int {
        eventHub.lock.withLock {
            eventHub.subscribers.values.filter { clientId == nil || $0.clientId == clientId }.count
        }
    }

    /// The battery changes (a `device` event follows).
    public func setBattery(percent: Int, charging: Bool) {
        eventHub.lock.withLock { eventHub.battery = .init(percent: percent, charging: charging) }
        checkDeviceChange()
    }

    /// Sends an event to every open stream (tests: unknown types...).
    public func sendEvent(type: String, json: Any) {
        emit(type, json)
    }

    /// Sends raw text to every open stream (tests of the parser).
    public func sendRawEvents(_ text: String) {
        for stream in eventHub.lock.withLock({ eventHub.subscribers.values.map(\.stream) }) {
            stream.send(Data(text.utf8))
        }
    }

    /// Ends every stream without `shutdown` (a slow client cut, a crash).
    public func dropEventStreams() {
        endEventStreams(shutdown: nil)
    }

    /// The user leaves ReminisLink on the console: `shutdown`, then the
    /// streams end, then every request fails to connect.
    public func quit() {
        availability = .gone
    }

    /// The pak dies without a word: its streams end without `shutdown` and
    /// it answers nothing more (its Bonjour service stays announced).
    public func vanish() {
        lock.withLock { _availability = .gone }
        endEventStreams(shutdown: nil)
    }

    // MARK: - Route

    var batteryJSON: Any {
        eventHub.lock.withLock { eventHub.battery }.map { ["percent": $0.percent, "charging": $0.charging] as [String: Any] }
            ?? NSNull()
    }

    var storageJSON: [[String: Any]] {
        [["id": "sd", "label": "SD", "totalBytes": 127_865_454_592, "freeBytes": freeBytes]]
    }

    func openEventStream(clientId: String) -> FakeResponse {
        let hub = eventHub
        let stream = FakeStream()
        let key = ObjectIdentifier(stream)
        var isFirst = false
        let refusal = hub.lock.withLock { () -> FakeResponse? in
            guard hub.supported else { return .error(404, code: "not_found", message: "No route") }
            let mine = hub.subscribers.values.filter { $0.clientId == clientId }.count
            guard mine < Self.maxEventStreamsPerClient, hub.subscribers.count < Self.maxEventStreams else {
                return .error(429, code: "too_many_attempts", message: "Too many event streams",
                              headers: ["Retry-After": String(hub.retryAfter)])
            }
            isFirst = hub.subscribers.isEmpty
            hub.subscribers[key] = FakeEventHub.Subscriber(clientId: clientId, stream: stream)
            return nil
        }
        if let refusal { return refusal }
        // No `device` event at the start (contract): the state with this
        // stream open is the reference, as the pak reads it then.
        if isFirst { resetDeviceReference() }
        stream.onClose = { [weak self] in self?.streamClosed(key) }
        send(to: key, "hello", ["serverTime": ProtocolJSON.formatDate(Date().addingTimeInterval(clockOffset)),
                                "keepAliveSeconds": Int(keepAliveInterval.rounded(.up))])
        restartEventTimers()
        var response = FakeResponse(status: 200, headers: ["Content-Type": "text/event-stream", "Cache-Control": "no-store"])
        response.stream = stream
        return response
    }

    private func streamClosed(_ key: ObjectIdentifier) {
        let removed = eventHub.lock.withLock { eventHub.subscribers.removeValue(forKey: key) != nil }
        guard removed else { return }
        afterStreamsChanged()
    }

    /// The streams changed: timers, and the presence of the others; the
    /// device reference is dropped with the last stream.
    private func afterStreamsChanged() {
        restartEventTimers()
        let none = eventHub.lock.withLock { () -> Bool in
            guard eventHub.subscribers.isEmpty else { return false }
            eventHub.lastDevice = nil
            eventHub.devicePending = false
            return true
        }
        if !none { checkDeviceChange() }
    }

    /// The current state becomes the reference of the `device` events.
    private func resetDeviceReference() {
        let snapshot = deviceSnapshot()
        eventHub.lock.withLock {
            eventHub.lastDevice = snapshot
            eventHub.devicePending = false
        }
    }

    private func deviceSnapshot() -> FakeEventHub.DeviceSnapshot {
        let clients = connectedClientsJSON().compactMap { $0["clientName"] as? String }
        let battery = eventHub.lock.withLock { eventHub.battery }
        return FakeEventHub.DeviceSnapshot(
            battery: battery ?? .init(percent: -1, charging: false), clients: clients, freeMiB: freeBytes >> 20
        )
    }

    /// Ends the streams, after a `shutdown` event when `reason` is set.
    func endEventStreams(shutdown reason: String?) {
        let streams = eventHub.lock.withLock { () -> [ObjectIdentifier] in
            Array(eventHub.subscribers.keys)
        }
        for key in streams {
            if let reason { send(to: key, "shutdown", ["reason": reason]) }
            let stream = eventHub.lock.withLock { eventHub.subscribers.removeValue(forKey: key)?.stream }
            stream?.finish()
        }
        afterStreamsChanged()
    }

    /// Ends the streams of a client (disconnected, forgotten, unpaired),
    /// without an event.
    func endEventStreams(clientId: String) {
        let streams = eventHub.lock.withLock { () -> [FakeStream] in
            let keys = eventHub.subscribers.filter { $0.value.clientId == clientId }.map(\.key)
            return keys.compactMap { eventHub.subscribers.removeValue(forKey: $0)?.stream }
        }
        for stream in streams { stream.finish() }
        afterStreamsChanged()
    }

    // MARK: - Emission

    private func send(to key: ObjectIdentifier, _ type: String, _ json: Any) {
        let payload = (try? JSONSerialization.data(withJSONObject: json, options: [.sortedKeys])) ?? Data("{}".utf8)
        let (stream, id) = eventHub.lock.withLock { () -> (FakeStream?, Int) in
            guard var subscriber = eventHub.subscribers[key] else { return (nil, 0) }
            let id = subscriber.nextId
            subscriber.nextId += 1
            eventHub.subscribers[key] = subscriber
            return (subscriber.stream, id)
        }
        guard let stream else { return }
        stream.send(Data("id: \(id)\nevent: \(type)\ndata: ".utf8) + payload + Data("\n\n".utf8))
    }

    func emit(_ type: String, _ json: Any) {
        for key in eventHub.lock.withLock({ Array(eventHub.subscribers.keys) }) {
            send(to: key, type, json)
        }
    }

    func emitJob(_ job: FakeJob) {
        emit("job", job.json)
    }

    /// Queues a `library` event: sent at once if the last one is a second
    /// old, otherwise once the second is over, with every change since
    /// merged (an unknown change, empty `systemIds`, wins).
    func emitLibrary(_ systemIds: [String]) {
        let hub = eventHub
        let delay = hub.lock.withLock { () -> TimeInterval? in
            let added: Set<String>? = systemIds.isEmpty ? nil : Set(systemIds)
            switch hub.pendingLibrary {
            case .none: hub.pendingLibrary = .some(added)
            case .some(let current):
                if let current, let added { hub.pendingLibrary = .some(current.union(added)) } else { hub.pendingLibrary = .some(nil) }
            }
            guard !hub.libraryScheduled else { return nil }
            hub.libraryScheduled = true
            let elapsed = hub.lastLibraryEmit.map { Date().timeIntervalSince($0) } ?? .infinity
            return max(0, hub.libraryInterval - elapsed)
        }
        guard let delay else { return }
        if delay == 0 {
            flushLibrary()
        } else {
            hub.queue.asyncAfter(deadline: .now() + delay) { [weak self] in self?.flushLibrary() }
        }
    }

    private func flushLibrary() {
        let hub = eventHub
        let pending = hub.lock.withLock { () -> Set<String>?? in
            defer {
                hub.pendingLibrary = .none
                hub.libraryScheduled = false
                hub.lastLibraryEmit = Date()
            }
            return hub.pendingLibrary
        }
        guard case .some(let systems) = pending else { return }
        emit("library", ["systemIds": systems.map { $0.sorted() } ?? []])
    }

    /// Queues a `device` event, at most one every ``deviceEventInterval``;
    /// its content is read when it is sent (the latest state).
    private func emitDevice() {
        let hub = eventHub
        let delay = hub.lock.withLock { () -> TimeInterval? in
            hub.devicePending = true
            guard !hub.deviceScheduled else { return nil }
            hub.deviceScheduled = true
            let elapsed = hub.lastDeviceEmit.map { Date().timeIntervalSince($0) } ?? .infinity
            return max(0, hub.deviceInterval - elapsed)
        }
        guard let delay else { return }
        if delay == 0 {
            flushDevice()
        } else {
            hub.queue.asyncAfter(deadline: .now() + delay) { [weak self] in self?.flushDevice() }
        }
    }

    private func flushDevice() {
        let hub = eventHub
        let pending = hub.lock.withLock { () -> Bool in
            defer {
                hub.devicePending = false
                hub.deviceScheduled = false
                hub.lastDeviceEmit = Date()
            }
            return hub.devicePending
        }
        guard pending else { return }
        emit("device", ["battery": batteryJSON, "connectedClients": connectedClientsJSON(), "storage": storageJSON])
    }

    /// A `device` event when the battery, the set of connected clients or
    /// the free space (by 1 MiB at least) changed since the last look. The
    /// first look is the reference (no event).
    func checkDeviceChange() {
        let snapshot = deviceSnapshot()
        let changed = eventHub.lock.withLock { () -> Bool in
            // No stream, no reference: the first stream sets it.
            guard !eventHub.subscribers.isEmpty, let last = eventHub.lastDevice else { return false }
            eventHub.lastDevice = snapshot
            return last != snapshot
        }
        guard changed else { return }
        emitDevice()
    }

    /// The systems whose games a request changed (nil: nothing changed).
    func libraryChange(method: String, path: String, response: FakeResponse, touchedSystem: String?) -> [String]? {
        guard method != "GET", (200..<300).contains(response.status) else { return nil }
        let prefix = APIClient.apiPrefix + "/"
        guard path.hasPrefix(prefix) else { return nil }
        let segments = path.dropFirst(prefix.count).split(separator: "/").map(String.init)
        switch segments.first {
        case "uploads" where segments.count == 3 && segments[2] == "complete":
            let object = (try? JSONSerialization.jsonObject(with: response.body)) as? [String: Any] ?? [:]
            if object["stagingId"] != nil || object["jobId"] != nil { return nil }
            if let gameId = object["gameId"] as? String, let (system, _) = library.game(id: gameId) {
                return [system.id]
            }
            return []
        case "games" where segments.count >= 2:
            // A patch changes the card when its job succeeds.
            if segments.count == 3 && segments[2] == "patch" { return nil }
            return touchedSystem.map { [$0] } ?? []
        case "trash":
            return []
        default:
            return nil
        }
    }

    /// The system of the game a request names, before it runs.
    func systemTouched(path: String) -> String? {
        let prefix = APIClient.apiPrefix + "/games/"
        guard path.hasPrefix(prefix) else { return nil }
        let rest = path.dropFirst(prefix.count)
        let gameId = String(rest.prefix { $0 != "/" })
        return library.game(id: gameId)?.0.id
    }

    /// The systems a succeeded job changed (nil for the staging area).
    func librarySystems(of job: FakeJob) -> [String]? {
        if let plan = job.patch {
            return library.game(id: plan.gameId).map { [$0.0.id] } ?? []
        }
        switch job.target {
        case .rom(let system, _, _): return [system.id]
        case .staging, .pak: return nil
        case .save, .state: return []
        }
    }

    // MARK: - Timers

    /// The keep-alive and job timers run while a stream is open.
    func restartEventTimers() {
        let hub = eventHub
        let (open, keepAlive, tick) = hub.lock.withLock { () -> (Bool, TimeInterval, TimeInterval?) in
            hub.keepAliveTimer?.cancel()
            hub.jobTimer?.cancel()
            hub.keepAliveTimer = nil
            hub.jobTimer = nil
            return (!hub.subscribers.isEmpty, hub.keepAliveInterval, hub.jobTickInterval)
        }
        guard open else { return }
        let keepAliveTimer = DispatchSource.makeTimerSource(queue: hub.queue)
        keepAliveTimer.schedule(deadline: .now() + keepAlive, repeating: keepAlive)
        keepAliveTimer.setEventHandler { [weak self] in self?.sendKeepAlive() }
        var jobTimer: DispatchSourceTimer?
        if let tick {
            let timer = DispatchSource.makeTimerSource(queue: hub.queue)
            timer.schedule(deadline: .now() + tick, repeating: tick)
            timer.setEventHandler { [weak self] in self?.tickJobs() }
            jobTimer = timer
        }
        hub.lock.withLock {
            hub.keepAliveTimer = keepAliveTimer
            hub.jobTimer = jobTimer
        }
        keepAliveTimer.resume()
        jobTimer?.resume()
    }

    private func sendKeepAlive() {
        let streams = eventHub.lock.withLock { () -> [FakeStream] in
            eventHub.keepAlivesPaused ? [] : eventHub.subscribers.values.map(\.stream)
        }
        for stream in streams { stream.send(Data(": keep-alive\n\n".utf8)) }
        checkDeviceChange()
    }

    /// One step of every unfinished job.
    private func tickJobs() {
        let finished: Set<String> = ["succeeded", "failed", "cancelled"]
        let ids = lock.withLock { jobs.values.filter { !finished.contains($0.state) }.map(\.id) }
        for id in ids { _ = advance(id) }
    }
}
#endif
