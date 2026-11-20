import Foundation
import ReminisLinkKit

/// Library changes merged while the debounce runs.
struct LibraryChange: Equatable {
    /// The systems touched, nil for all of them.
    var systems: Set<String>?

    mutating func merge(_ other: Set<String>?) {
        guard let current = systems, let other else {
            systems = nil
            return
        }
        systems = current.union(other)
    }
}

/// Milestone M14: the event stream of the console (`GET /events`).
///
/// It runs while the session is connected, its console is the one shown
/// and the app is active; it is stopped in the background and when the
/// app goes back to the list or to another console. What it brings:
/// - `shutdown` (or the stream ending right after it): the console closed
///   ReminisLink, back to the list at once, uploads paused (no probe);
/// - the stream ending without `shutdown`: the M11 `/hello` probe decides
///   (closed, or reconnect);
/// - 35 s without a byte: the connection is lost (as a network failure);
/// - `library`: the screens reload (debounced, by system when known);
/// - `job`: jobs follow the events, the screens stop polling;
/// - `device`: the dashboard (battery, connected devices, storage).
/// A pak without `/events` (`404`) keeps the polling for this session.
extension ConsoleSession {
    /// Whether the stream should run now.
    private var wantsEventStream: Bool {
        guard let app = liveApp else { return false }
        return client != nil && state == .connected && app.isForeground && app.openConsoleId == deviceId
            && !eventsUnsupported && !closedByConsole
    }

    /// Starts or stops the stream to match the session and the app.
    func updateEventStream() {
        guard wantsEventStream, let client else {
            stopEventStream()
            return
        }
        if let events, events.isRunning, eventsClient === client { return }
        stopEventStream()
        startEventStream(client: client)
    }

    func stopEventStream() {
        // A catch-up of this stream publishes nothing after it closed.
        catchUpGeneration += 1
        catchUpTask?.cancel()
        catchUpTask = nil
        events?.stop()
        events = nil
        eventsClient = nil
        setEventsLive(false)
    }

    private func startEventStream(client: APIClient) {
        guard let app = liveApp else { return }
        let stream = EventStream(
            timing: app.eventTiming,
            connect: { try await client.openEvents() },
            onEvent: { [weak self] event in self?.handle(event, from: client) },
            onEnd: { [weak self] end in await self?.eventStreamEnded(end, client: client) ?? .stop }
        )
        events = stream
        eventsClient = client
        stream.start()
    }

    private func setEventsLive(_ live: Bool) {
        if eventsLive != live { eventsLive = live }
        if transfers.jobsFollowedByEvents != live { transfers.jobsFollowedByEvents = live }
    }

    private func handle(_ event: PakEvent, from client: APIClient) {
        guard liveApp != nil, self.client === client else { return }
        switch event {
        case .hello:
            setEventsLive(true)
            // Only the last catch-up publishes (generation, not ordering).
            catchUpGeneration += 1
            let generation = catchUpGeneration
            catchUpTask?.cancel()
            catchUpTask = Task { await self.catchUp(client: client, generation: generation) }
        case .device(let device):
            lastDeviceEvent = device
            deviceEvents += 1
            status = status?.updated(with: device)
        case .library(let library):
            libraryChanged(library.affectedSystems)
        case .job(let job):
            transfers.apply(job)
        case .shutdown, .unknown:
            break
        }
    }

    /// After every `hello`: the dashboard is read once (no `device` event
    /// is sent at the start), the jobs once, and the screens (library, open
    /// game, trash) reload: the pak replays nothing, and whatever happened
    /// before this stream (background, the list shown, a slow opening) is
    /// unknown. Cheap, and never stale. A newer `hello` or the end of the
    /// stream makes this one publish nothing more.
    private func catchUp(client: APIClient, generation: Int) async {
        func isCurrent() -> Bool {
            liveApp != nil && self.client === client && catchUpGeneration == generation && !Task.isCancelled
        }
        let eventsBefore = deviceEvents
        if let status = try? await client.device(), isCurrent() {
            // A `device` event that arrived while the request was out is
            // newer than this answer: its fields win.
            if deviceEvents != eventsBefore, let event = lastDeviceEvent {
                self.status = status.updated(with: event)
            } else {
                self.status = status
            }
        }
        guard isCurrent() else { return }
        await transfers.refreshJobs(force: true)
        guard isCurrent() else { return }
        lastLibraryChange = nil
        libraryRevision += 1
        await loadTrash()
        if isCurrent() { catchUpsDone += 1 }
    }

    /// A `library` event: the screens reload once the events stop coming
    /// for a moment.
    private func libraryChanged(_ systems: Set<String>?) {
        if pendingLibraryChange != nil {
            pendingLibraryChange?.merge(systems)
            return
        }
        guard let app = liveApp else { return }
        pendingLibraryChange = LibraryChange(systems: systems)
        let debounce = app.libraryEventDebounce
        Task { [weak self] in
            try? await Task.sleep(for: debounce)
            self?.applyLibraryChange()
        }
    }

    private func applyLibraryChange() {
        guard liveApp != nil, let change = pendingLibraryChange else { return }
        pendingLibraryChange = nil
        lastLibraryChange = change.systems
        libraryRevision += 1
        Task { await loadTrash() }
    }

    /// Whether the last library change may concern a system (unknown
    /// system or unknown change: yes).
    func libraryChangeAffects(_ systemId: String?) -> Bool {
        guard let systemId, let systems = lastLibraryChange else { return true }
        return systems.contains(systemId)
    }

    /// A connection of the stream ended: what the session makes of it.
    private func eventStreamEnded(_ end: EventStream.End, client: APIClient) async -> EventStream.Decision {
        guard liveApp != nil, self.client === client else { return .stop }
        setEventsLive(false)
        switch end {
        case .shutdown:
            consoleClosed(client: client)
            return .stop
        case .livenessLost:
            // Asleep, out of Wi-Fi range: as a network failure today.
            connectionLost(.api(.network(.timedOut)), client: client)
            return .stop
        case .endOfStream:
            // Not announced: the M11 probe tells a closing pak from a
            // stream cut for another reason (slow reader, restart).
            do {
                try await client.hello(timeout: PakClosure.probeTimeout)
            } catch {
                guard liveApp != nil else { return .stop }
                if PakClosure.probeMeansClosed(error) {
                    consoleClosed(client: client)
                    return .stop
                }
            }
            return wantsEventStream && self.client === client ? .reconnect : .stop
        case .failed(let error):
            return streamFailed(error, client: client)
        }
    }

    private func streamFailed(_ error: APIError, client: APIClient) -> EventStream.Decision {
        let status: Int? = switch error {
        case .server(let server): server.status
        case .unexpectedStatus(let status): status
        default: nil
        }
        if status == 404 {
            // A pak older than M14: polling, as before, for this session.
            eventsUnsupported = true
            return .stop
        }
        guard let app = liveApp else { return .stop }
        if app.meansConsoleClosed(error, deviceId: deviceId) {
            consoleClosed(client: client)
            return .stop
        }
        if error.code == .unknownClient || error.code == .disconnected {
            // Already seen by the error observer for `disconnected`.
            connectionLost(.api(error), client: client)
            return .stop
        }
        // `429` (after `Retry-After`), a network failure, an unexpected
        // answer: again after the backoff; the liveness timeout ends it.
        return wantsEventStream && self.client === client ? .reconnect : .stop
    }
}
