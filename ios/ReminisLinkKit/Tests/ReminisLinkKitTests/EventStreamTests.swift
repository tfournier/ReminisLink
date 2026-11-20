import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// A clock the test moves by hand: sleepers wake only when it advances.
final class ManualTime: @unchecked Sendable {
    private let lock = NSLock()
    private var current: Duration = .zero
    private var sleepers: [UUID: (deadline: Duration, continuation: CheckedContinuation<Void, Error>)] = [:]

    var now: Duration { lock.withLock { current } }
    var sleeperCount: Int { lock.withLock { sleepers.count } }

    func sleep(_ duration: Duration) async throws {
        let id = UUID()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                if Task.isCancelled {
                    continuation.resume(throwing: CancellationError())
                    return
                }
                let wakeNow = lock.withLock { () -> Bool in
                    let deadline = current + duration
                    guard deadline > current else { return true }
                    sleepers[id] = (deadline, continuation)
                    return false
                }
                if wakeNow { continuation.resume() }
            }
        } onCancel: {
            let continuation = lock.withLock { sleepers.removeValue(forKey: id)?.continuation }
            continuation?.resume(throwing: CancellationError())
        }
    }

    /// Moves the clock and wakes the sleepers whose time came.
    func advance(by duration: Duration) async {
        let due = lock.withLock { () -> [CheckedContinuation<Void, Error>] in
            current += duration
            let ids = sleepers.filter { $0.value.deadline <= current }.map(\.key)
            return ids.compactMap { sleepers.removeValue(forKey: $0)?.continuation }
        }
        for continuation in due { continuation.resume() }
        await settle()
    }

    func timing(liveness: Duration = .seconds(35)) -> EventStreamTiming {
        EventStreamTiming(livenessTimeout: liveness, now: { self.now }, sleep: { try await self.sleep($0) })
    }
}

/// Lets the tasks woken so far run.
func settle() async {
    for _ in 0..<50 { await Task.yield() }
    try? await Task.sleep(for: .milliseconds(5))
    for _ in 0..<50 { await Task.yield() }
}

/// Waits (real time, bounded) until a condition holds.
@MainActor
func until(_ condition: @MainActor () -> Bool) async {
    for _ in 0..<400 where !condition() {
        try? await Task.sleep(for: .milliseconds(5))
    }
}

/// Hands out connections the test feeds by hand.
final class TestConnector: @unchecked Sendable {
    private let lock = NSLock()
    private var continuations: [AsyncThrowingStream<UInt8, Error>.Continuation] = []
    private var failures: [Int: Error] = [:]
    private var _connects = 0

    var connects: Int { lock.withLock { _connects } }

    /// The `n`-th connection (from 1) fails with `error`.
    func fail(attempt n: Int, with error: Error) {
        lock.withLock { failures[n] = error }
    }

    /// Every connection from now on fails with `error`.
    func failAll(with error: Error) {
        lock.withLock { for n in 1...100 { failures[n] = error } }
    }

    func connect() async throws -> any EventByteSource {
        let (n, failure) = lock.withLock { () -> (Int, Error?) in
            _connects += 1
            return (_connects, failures[_connects])
        }
        _ = n
        if let failure { throw failure }
        let (stream, continuation) = AsyncThrowingStream.makeStream(of: UInt8.self)
        lock.withLock { continuations.append(continuation) }
        return SequenceByteSource(stream, onCancel: { continuation.finish() })
    }

    func send(_ text: String) {
        guard let continuation = lock.withLock({ continuations.last }) else { return }
        for byte in text.utf8 { continuation.yield(byte) }
    }

    func end(throwing error: Error? = nil) {
        guard let continuation = lock.withLock({ continuations.last }) else { return }
        if let error { continuation.finish(throwing: error) } else { continuation.finish() }
    }
}

/// What a test stream reported.
@MainActor
final class StreamRecorder {
    var events: [PakEvent] = []
    var ends: [EventStream.End] = []
    var decision: EventStream.Decision = .reconnect
}

@MainActor
@Suite("Event stream")
struct EventStreamTests {
    static let hello = "id: 1\nevent: hello\ndata: {\"serverTime\":\"2026-11-20T08:00:00Z\",\"keepAliveSeconds\":15}\n\n"

    func makeStream(_ time: ManualTime, _ connector: TestConnector, _ recorder: StreamRecorder) -> EventStream {
        EventStream(
            timing: time.timing(),
            connect: { try await connector.connect() },
            onEvent: { recorder.events.append($0) },
            onEnd: { end in
                recorder.ends.append(end)
                return recorder.decision
            }
        )
    }

    @Test func eventsArriveInOrderAndUnknownOnesPassAsUnknown() async {
        let time = ManualTime(), connector = TestConnector(), recorder = StreamRecorder()
        let stream = makeStream(time, connector, recorder)
        stream.start()
        await until { connector.connects == 1 }
        connector.send(Self.hello + ": keep-alive\n\nevent: future\ndata: 1\n\nevent: library\r\ndata: {\"systemIds\":[\"a\"]}\r\n\r\n")
        await until { recorder.events.count == 3 }
        #expect(recorder.events == [
            .hello(EventHello(serverTime: Date(timeIntervalSince1970: 1_795_161_600), keepAliveSeconds: 15)),
            .unknown("future"),
            .library(EventLibrary(systemIds: ["a"])),
        ])
        #expect(stream.isLive)
        stream.stop()
    }

    @Test func thirtyFiveSecondsWithoutAByteLoseTheConnection() async {
        let time = ManualTime(), connector = TestConnector(), recorder = StreamRecorder()
        let stream = makeStream(time, connector, recorder)
        stream.start()
        await until { connector.connects == 1 && time.sleeperCount == 1 }
        connector.send(Self.hello)
        await until { stream.isLive }
        await time.advance(by: .seconds(30))
        // A keep-alive (a comment) is a byte: the timer starts again.
        let before = stream.bytesReceived
        connector.send(": keep-alive\n\n")
        await until { stream.bytesReceived == before + 14 }
        await time.advance(by: .seconds(30))
        // The watchdog woke at 35 s, saw a byte 30 s ago: sleeps 5 s more.
        await until { time.sleeperCount == 1 }
        #expect(recorder.ends.isEmpty)
        #expect(stream.isRunning)
        await time.advance(by: .seconds(5))
        await until { !recorder.ends.isEmpty }
        #expect(recorder.ends == [.livenessLost])
        #expect(!stream.isRunning)
        #expect(connector.connects == 1)
    }

    @Test func aLiveStreamThatEndsReconnectsAfterTheInitialDelay() async {
        let time = ManualTime(), connector = TestConnector(), recorder = StreamRecorder()
        let stream = makeStream(time, connector, recorder)
        stream.start()
        await until { connector.connects == 1 }
        connector.send(Self.hello)
        await until { stream.isLive }
        connector.end()
        await until { recorder.ends.count == 1 }
        #expect(recorder.ends == [.endOfStream])
        #expect(!stream.isLive)
        // The watchdog and the backoff sleep.
        await until { time.sleeperCount == 2 }
        await time.advance(by: .milliseconds(999))
        #expect(connector.connects == 1)
        await time.advance(by: .milliseconds(1))
        await until { connector.connects == 2 }
        #expect(connector.connects == 2)
        stream.stop()
    }

    @Test func failuresInARowBackOffAndEndInALostConnection() async {
        let time = ManualTime(), connector = TestConnector(), recorder = StreamRecorder()
        connector.failAll(with: APIError.network(.cannotConnectToHost))
        let stream = makeStream(time, connector, recorder)
        stream.start()
        // 2 s, 4 s, 8 s, 15 s (the maximum), then the liveness timeout.
        var elapsed: Duration = .zero
        for (attempt, delay) in [(1, 2), (2, 4), (3, 8), (4, 15)] {
            await until { connector.connects == attempt && recorder.ends.count == attempt }
            #expect(recorder.ends.last == .failed(.network(.cannotConnectToHost)))
            await until { time.sleeperCount == 2 }
            await time.advance(by: .seconds(delay) - .milliseconds(1))
            #expect(connector.connects == attempt)
            await time.advance(by: .milliseconds(1))
            elapsed += .seconds(delay)
        }
        await until { connector.connects == 5 }
        #expect(elapsed == .seconds(29))
        await time.advance(by: .seconds(6))
        await until { recorder.ends.last == .livenessLost }
        #expect(recorder.ends.last == .livenessLost)
        #expect(!stream.isRunning)
    }

    @Test func tooManyStreamsWaitsForRetryAfter() async {
        let time = ManualTime(), connector = TestConnector(), recorder = StreamRecorder()
        let busy = ServerError(status: 429, code: .tooManyAttempts, message: "Too many event streams", details: [:], retryAfter: 7)
        connector.fail(attempt: 1, with: APIError.server(busy))
        let unannounced = ServerError(status: 429, code: .tooManyAttempts, message: "Too many event streams", details: [:], retryAfter: nil)
        connector.fail(attempt: 2, with: APIError.server(unannounced))
        let stream = makeStream(time, connector, recorder)
        stream.start()
        await until { recorder.ends.count == 1 && time.sleeperCount == 2 }
        await time.advance(by: .milliseconds(6999))
        #expect(connector.connects == 1)
        await time.advance(by: .milliseconds(1))
        await until { recorder.ends.count == 2 && time.sleeperCount == 2 }
        #expect(connector.connects == 2)
        // Without Retry-After: 5 s.
        await time.advance(by: .milliseconds(4999))
        #expect(connector.connects == 2)
        await time.advance(by: .milliseconds(1))
        await until { connector.connects == 3 }
        #expect(connector.connects == 3)
        // An error answer is a sign of life: no liveness loss meanwhile.
        #expect(!recorder.ends.contains(.livenessLost))
        stream.stop()
    }

    @Test func shutdownEndsTheConnectionAtOnce() async {
        let time = ManualTime(), connector = TestConnector(), recorder = StreamRecorder()
        recorder.decision = .stop
        let stream = makeStream(time, connector, recorder)
        stream.start()
        await until { connector.connects == 1 }
        // The stream stays open after `shutdown`: nothing more is read.
        connector.send(Self.hello + "id: 2\nevent: shutdown\ndata: {\"reason\":\"quit\"}\n\nid: 3\nevent: library\ndata: {\"systemIds\":[]}\n\n")
        await until { !recorder.ends.isEmpty }
        #expect(recorder.ends == [.shutdown(EventShutdown(reason: "quit"))])
        #expect(recorder.events.count == 1)
        #expect(!stream.isRunning)
    }

    @Test func stopReportsNothingMore() async {
        let time = ManualTime(), connector = TestConnector(), recorder = StreamRecorder()
        let stream = makeStream(time, connector, recorder)
        stream.start()
        await until { connector.connects == 1 }
        stream.stop()
        connector.send(Self.hello)
        connector.end()
        await time.advance(by: .seconds(60))
        #expect(recorder.events.isEmpty)
        #expect(recorder.ends.isEmpty)
    }

    @Test func theDelaysDoubleUpToTheMaximum() {
        let timing = EventStreamTiming(now: { .zero }, sleep: { _ in })
        #expect((0...6).map { timing.reconnectDelay(failures: $0) } == [1, 2, 4, 8, 15, 15, 15].map { Duration.seconds($0) })
        #expect(timing.reconnectDelay(failures: 3, retryAfter: .seconds(9)) == .seconds(9))
    }

    // MARK: - Against the fake pak

    @Test func theSignedStreamStartsWithHelloAndKeepsAlive() async throws {
        let pak = makePak()
        pak.keepAliveInterval = 0.05
        let client = try await pairedClient(pak)
        let bytes = try await client.openEvents()
        var parser = SSEParser()
        var items: [SSEItem] = []
        while let byte = try await bytes.next() {
            if let item = parser.feed(byte) { items.append(item) }
            if items.filter({ $0 == .comment("keep-alive") }).count == 2 { break }
        }
        guard case .event(let first) = items.first else { Issue.record("no event"); return }
        #expect(first.type == "hello")
        #expect(first.id == "1")
        #expect(pak.log.contains { $0.path == "/api/v1/events" && $0.status == 200 && $0.nonce != nil })
    }

    @Test func anOlderPakAnswers404AndTheLimitsAnswer429() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        pak.eventsSupported = false
        await #expect { try await client.openEvents() } throws: { error in
            (error as? APIError).flatMap { if case .server(let e) = $0 { e.status } else { nil } } == 404
        }
        pak.eventsSupported = true
        var open: [any EventByteSource] = []
        for _ in 0..<FakePak.maxEventStreamsPerClient { open.append(try await client.openEvents()) }
        await #expect { try await client.openEvents() } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 429 && e.code == .tooManyAttempts && e.retryAfter == 5
        }
        #expect(pak.eventStreamCount() == FakePak.maxEventStreamsPerClient)
        _ = open
    }

    @Test func aWriteOfAnotherClientIsAnnounced() async throws {
        let pak = makePak()
        let watcher = try await pairedClient(pak)
        let other = try await pairedClient(pak)
        let bytes = try await watcher.openEvents()
        let gameId = DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles.hab")
        let systemId = try #require(pak.library.game(id: gameId)?.0.id)
        _ = try await other.deleteGame(id: gameId, keepSave: false)
        var parser = SSEParser()
        var library: SSEEvent?
        while let byte = try await bytes.next() {
            if case .event(let event)? = parser.feed(byte), event.type == "library" {
                library = event
                break
            }
        }
        #expect(library.flatMap { PakEvent($0) } == .library(EventLibrary(systemIds: [systemId])))
    }

    /// Reads events of one type from a stream, with the time each arrived.
    func read(_ type: String, count: Int, from bytes: any EventByteSource) async throws -> [(event: PakEvent, at: ContinuousClock.Instant)] {
        var parser = SSEParser()
        var found: [(event: PakEvent, at: ContinuousClock.Instant)] = []
        while found.count < count, let byte = try await bytes.next() {
            if case .event(let raw)? = parser.feed(byte), raw.type == type, let event = PakEvent(raw) {
                found.append((event, .now))
            }
        }
        return found
    }

    @Test func libraryEventsComeAtMostOncePerIntervalWithChangesMerged() async throws {
        let pak = makePak()
        // Long: nothing goes by the clock; the test says when it passed.
        pak.libraryEventInterval = 3600
        let watcher = try await pairedClient(pak)
        let other = try await pairedClient(pak)
        let bytes = try await watcher.openEvents()
        func system(_ path: String) throws -> (game: String, system: String) {
            let id = DemoLibrary.id(path)
            return (id, try #require(pak.library.game(id: id)?.0.id))
        }
        let gba = try system("Handheld Advance (HA)/Brambleway Dash.hab")
        let gb = try system("01) Pocket Classic (PCL)/Mosslight Maze 🌙.pcr")
        let gba2 = try system("Handheld Advance (HA)/Thistledown Isles.hab")
        _ = try await other.deleteGame(id: gba.game, keepSave: false)
        #expect(!pak.libraryEventPending)
        _ = try await other.deleteGame(id: gb.game, keepSave: false)
        _ = try await other.deleteGame(id: gba2.game, keepSave: false)
        // Held back within the interval.
        #expect(pak.libraryEventPending)
        pak.flushThrottledEvents()
        // One at once, the next two merged into one.
        let events = try await read("library", count: 2, from: bytes)
        #expect(events.map(\.event) == [
            .library(EventLibrary(systemIds: [gba.system])),
            .library(EventLibrary(systemIds: [gba.system, gb.system].sorted())),
        ])
        // An unknown change (the trash) wins over the systems.
        _ = try await other.deleteGame(id: DemoLibrary.id("Handheld Advance (HA)/Homebrew/Quillwork Tactics.hab"), keepSave: false)
        _ = try await other.emptyTrash()
        pak.flushThrottledEvents()
        let merged = try await read("library", count: 1, from: bytes)
        #expect(merged.map(\.event) == [.library(EventLibrary(systemIds: []))])
    }

    @Test func deviceEventsComeAtMostOncePerIntervalWithTheLatestState() async throws {
        let pak = makePak()
        pak.deviceEventInterval = 3600
        let watcher = try await pairedClient(pak)
        let bytes = try await watcher.openEvents()
        pak.setBattery(percent: 10, charging: false)
        // The first change goes at once (this one, or the presence of the
        // new stream).
        _ = try await read("device", count: 1, from: bytes)
        pak.setBattery(percent: 20, charging: false)
        pak.setBattery(percent: 30, charging: true)
        pak.flushThrottledEvents()
        let next = try await read("device", count: 1, from: bytes)
        guard case .device(let device) = next.first?.event else { Issue.record("no device event"); return }
        // One event, read when sent: the last battery, never 20.
        #expect(device.battery == .init(percent: 30, charging: true))
    }

    @Test func noDeviceEventFollowsHello() async throws {
        let pak = makePak()
        pak.deviceEventInterval = 0
        let client = try await pairedClient(pak)
        // Its first signed request is the stream's: opening it changes the
        // connected clients, which must not give a `device` event.
        let bytes = try await client.openEvents()
        _ = try await client.systems()
        pak.sendEvent(type: "marker", json: [String: Any]())
        var parser = SSEParser()
        var types: [String] = []
        while let byte = try await bytes.next() {
            if case .event(let event)? = parser.feed(byte) {
                types.append(event.type)
                if event.type == "marker" { break }
            }
        }
        // The presence of the new stream is the reference, not a change.
        #expect(types == ["hello", "marker"])
        // A real change still goes.
        pak.setBattery(percent: 5, charging: false)
        let device = try await read("device", count: 1, from: bytes)
        #expect(device.count == 1)
    }
}
