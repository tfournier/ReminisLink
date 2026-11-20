import Foundation

/// The delays of an ``EventStream``, and the clock it measures them with
/// (tests drive their own).
public struct EventStreamTiming: Sendable {
    /// No byte for this long (keep-alives included): the connection is
    /// lost (veille, Wi-Fi cut). The pak sends one at least every 15 s.
    public var livenessTimeout: Duration
    /// The wait before reconnecting after a stream that was live.
    public var initialBackoff: Duration
    /// The longest wait between two failed attempts.
    public var maxBackoff: Duration
    /// The wait after `429` without `Retry-After`.
    public var defaultRetryAfter: Duration
    /// Time since some fixed origin.
    public var now: @Sendable () -> Duration
    public var sleep: @Sendable (Duration) async throws -> Void

    public init(
        livenessTimeout: Duration = .seconds(35),
        initialBackoff: Duration = .seconds(1),
        maxBackoff: Duration = .seconds(15),
        defaultRetryAfter: Duration = .seconds(5),
        now: @escaping @Sendable () -> Duration,
        sleep: @escaping @Sendable (Duration) async throws -> Void
    ) {
        self.livenessTimeout = livenessTimeout
        self.initialBackoff = initialBackoff
        self.maxBackoff = maxBackoff
        self.defaultRetryAfter = defaultRetryAfter
        self.now = now
        self.sleep = sleep
    }

    /// The real clock.
    public static var live: EventStreamTiming {
        let origin = ContinuousClock.now
        return EventStreamTiming(now: { ContinuousClock.now - origin }, sleep: { try await Task.sleep(for: $0) })
    }

    /// The wait before the next attempt: `Retry-After` after `429`;
    /// otherwise ``initialBackoff`` doubled for each attempt that failed
    /// in a row since the stream was last live, at most ``maxBackoff``.
    public func reconnectDelay(failures: Int, retryAfter: Duration? = nil) -> Duration {
        if let retryAfter { return max(.zero, retryAfter) }
        var delay = initialBackoff
        for _ in 0..<max(0, failures) {
            delay = delay * 2
            if delay >= maxBackoff { return maxBackoff }
        }
        return min(delay, maxBackoff)
    }
}

/// The event stream of a console (`GET /events`, milestone M14): opens it,
/// parses it, watches its liveness and reconnects as its owner decides.
///
/// - Every (re)connection calls `connect`, which signs a new request.
/// - Liveness: any byte (an event, `: keep-alive`, an error answer)
///   counts; ``EventStreamTiming/livenessTimeout`` without one, across
///   reconnections, ends the stream with ``End/livenessLost``.
/// - When a connection ends, `onEnd` decides: reconnect (after the
///   backoff, or `Retry-After` for `429`) or stop.
/// - `shutdown` ends the connection at once (nothing is read after it).
/// - Unknown event types and unreadable data are ignored.
@MainActor
public final class EventStream {
    public enum End: Sendable, Equatable {
        /// `shutdown` arrived: the pak is closing.
        case shutdown(EventShutdown)
        /// The pak ended the stream without `shutdown`.
        case endOfStream
        /// The stream could not open (an error answer, no connection) or
        /// broke while open.
        case failed(APIError)
        /// Nothing received for ``EventStreamTiming/livenessTimeout``.
        /// Final: `onEnd`'s decision is ignored.
        case livenessLost
    }

    public enum Decision: Sendable, Equatable {
        case reconnect
        case stop
    }

    public typealias Connect = @Sendable () async throws -> AsyncThrowingStream<UInt8, Error>

    public let timing: EventStreamTiming
    private let connect: Connect
    private let onEvent: @MainActor (PakEvent) -> Void
    private let onEnd: @MainActor (End) async -> Decision

    public private(set) var isRunning = false
    /// `hello` arrived on the current connection.
    public private(set) var isLive = false
    /// Connections tried since the start.
    public private(set) var attempts = 0

    private var loopTask: Task<Void, Never>?
    private var watchdogTask: Task<Void, Never>?
    private var generation = 0
    private let activity = Activity()

    public init(
        timing: EventStreamTiming = .live,
        connect: @escaping Connect,
        onEvent: @escaping @MainActor (PakEvent) -> Void,
        onEnd: @escaping @MainActor (End) async -> Decision
    ) {
        self.timing = timing
        self.connect = connect
        self.onEvent = onEvent
        self.onEnd = onEnd
    }

    public func start() {
        guard !isRunning else { return }
        isRunning = true
        generation += 1
        let current = generation
        activity.touch(timing.now())
        loopTask = Task { [weak self] in await self?.loop(current) }
        let timing = timing
        let activity = activity
        watchdogTask = Task { [weak self] in
            while !Task.isCancelled {
                let elapsed = timing.now() - activity.last
                if elapsed >= timing.livenessTimeout {
                    await self?.livenessExpired(current)
                    return
                }
                do { try await timing.sleep(timing.livenessTimeout - elapsed) } catch { return }
            }
        }
    }

    /// Stops at once; nothing is reported after this.
    public func stop() {
        guard isRunning else { return }
        isRunning = false
        isLive = false
        generation += 1
        loopTask?.cancel()
        watchdogTask?.cancel()
        loopTask = nil
        watchdogTask = nil
    }

    private func livenessExpired(_ current: Int) async {
        guard isRunning, generation == current else { return }
        stop()
        _ = await onEnd(.livenessLost)
    }

    private func loop(_ current: Int) async {
        var failures = 0
        while !Task.isCancelled, generation == current {
            attempts += 1
            let (end, wasLive) = await runOnce(current)
            guard !Task.isCancelled, generation == current else { return }
            isLive = false
            failures = wasLive ? 0 : failures + 1
            let decision = await onEnd(end)
            guard !Task.isCancelled, generation == current else { return }
            guard decision == .reconnect else {
                stop()
                return
            }
            var retryAfter: Duration?
            if case .failed(.server(let error)) = end, error.status == 429 {
                retryAfter = error.retryAfter.map { .seconds($0) } ?? timing.defaultRetryAfter
            }
            do {
                try await timing.sleep(timing.reconnectDelay(failures: wasLive ? 0 : failures, retryAfter: retryAfter))
            } catch {
                return
            }
        }
    }

    /// One connection, until it ends. Returns how, and whether it was live.
    private func runOnce(_ current: Int) async -> (End, Bool) {
        var wasLive = false
        do {
            let bytes = try await connect()
            activity.touch(timing.now())
            var parser = SSEParser()
            for try await byte in bytes {
                activity.touch(timing.now())
                guard let item = parser.feed(byte), case .event(let raw) = item, let event = PakEvent(raw) else { continue }
                guard generation == current else { return (.endOfStream, wasLive) }
                switch event {
                case .shutdown(let shutdown):
                    return (.shutdown(shutdown), wasLive)
                case .hello:
                    wasLive = true
                    isLive = true
                default:
                    break
                }
                onEvent(event)
            }
            return (.endOfStream, wasLive)
        } catch let error as APIError {
            // An answer, even an error, shows the console is reachable.
            if case .server = error { activity.touch(timing.now()) }
            if case .unexpectedStatus = error { activity.touch(timing.now()) }
            return (.failed(error), wasLive)
        } catch is CancellationError {
            return (.endOfStream, wasLive)
        } catch let error as URLError {
            return (.failed(.network(error.code)), wasLive)
        } catch {
            return (.failed(.network(.unknown)), wasLive)
        }
    }

    /// The last moment a byte arrived.
    private final class Activity: @unchecked Sendable {
        private let lock = NSLock()
        private var _last: Duration = .zero

        var last: Duration { lock.withLock { _last } }

        func touch(_ now: Duration) {
            lock.withLock { _last = now }
        }
    }
}
