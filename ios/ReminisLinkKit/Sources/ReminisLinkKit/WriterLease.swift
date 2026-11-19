import Foundation
import Observation

/// Who may write to the SD card of a console, as seen by this iPhone
/// (milestone M10, single writer).
public enum WriterAccess: Equatable, Sendable {
    /// Not known yet. Writes are allowed: the pak decides.
    case unknown
    /// Nobody holds the lease: the next write (or renewal) takes it.
    case free
    /// This iPhone holds the lease.
    case writer
    /// Another client holds the lease. `tookOver`: this iPhone held it and
    /// the other client took it ("<device> took over").
    case readOnly(holder: String, tookOver: Bool)
    /// The pak has no write lease (before M10): writes are allowed.
    case unsupported

    public var isReadOnly: Bool {
        if case .readOnly = self { return true }
        return false
    }

    /// The client holding the lease, when it is another one.
    public var holder: String? {
        if case .readOnly(let holder, _) = self { return holder }
        return nil
    }
}

/// Keeps the write lease of one console (milestone M10).
///
/// While the app is in the foreground, or while one of its transfers is
/// running, it sends `PUT /writer` (without `force`) about every
/// ``defaultRenewInterval``: this renews the lease when this iPhone holds
/// it and takes it when it is free, and never takes it from another
/// client (the pak answers `423 read_only`, and the state becomes
/// read-only). In the background with no transfer running, it sends
/// `DELETE /writer` once and stops. ``takeOver()`` is the only path that
/// sends `force`.
///
/// Two rules keep it simple:
/// - **One lease request at a time.** `PUT` and `DELETE /writer` go through
///   a serial queue, and a request is never cancelled once queued: a
///   release is sent only after every acquisition sent before it has been
///   answered, so the pak always handles them in that order and a release
///   can never be overtaken by an acquisition.
/// - **The newest request wins.** Every change of state carries the moment
///   its request was sent; an answer to an older request than the one the
///   state comes from is ignored. `423 read_only` answers seen elsewhere
///   (any write of the app) come in through ``noteReadOnly(holder:sentAt:)``
///   under the same rule.
@MainActor
@Observable
public final class WriterLease {
    public static let defaultRenewInterval: Duration = .seconds(20)

    public private(set) var access: WriterAccess = .unknown
    /// Expiry of the lease held by this iPhone, as the pak gave it.
    public private(set) var expiresAt: Date?
    public private(set) var isForeground: Bool
    /// A renewal loop is running.
    public var isRunning: Bool { loop != nil }

    public var isReadOnly: Bool { access.isReadOnly }

    /// Called whenever this iPhone gets the lease (taken, taken over, or
    /// back after a reconnection): uploads paused by `423` resume.
    @ObservationIgnored public var onAcquired: (() -> Void)?
    /// Called when a lease request meets an error that ends the session
    /// (`401 disconnected`, `401 unknown_client`...). The loop stops.
    @ObservationIgnored public var onFatalError: ((APIError) -> Void)?
    /// A lease request did not reach the console (M11: with the Bonjour
    /// service gone, the console closed ReminisLink).
    @ObservationIgnored public var onConnectionFailure: ((APIError) -> Void)?

    @ObservationIgnored private let renewInterval: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let client: () -> APIClient?
    @ObservationIgnored private let transfersRunning: () -> Bool
    @ObservationIgnored private var loop: Task<Void, Never>?
    @ObservationIgnored private var stopped = false
    /// When the request the current state comes from was sent.
    @ObservationIgnored private var stateSentAt: ContinuousClock.Instant = .now
    /// The last lease request queued: the next one runs after it.
    @ObservationIgnored private var queueTail: Task<Void, Never>?
    /// `PUT /writer` sent since the last `DELETE /writer`: one of them may
    /// have granted the lease even if its answer was an error or lost.
    @ObservationIgnored private var acquisitionsSinceRelease = 0

    /// The lease should be held now (foreground, or a transfer running).
    private var wantsLease: Bool { !stopped && (isForeground || transfersRunning()) }

    /// - Parameters:
    ///   - client: the signed client of the current connection, if any
    ///     (never connects by itself).
    ///   - transfersRunning: an upload or job of this console is running.
    ///   - sleep: waits between two renewals (tests pass a manual clock).
    public init(
        isForeground: Bool,
        renewInterval: Duration = WriterLease.defaultRenewInterval,
        sleep: @escaping @Sendable (Duration) async throws -> Void = { try await Task.sleep(for: $0) },
        client: @escaping () -> APIClient?,
        transfersRunning: @escaping () -> Bool
    ) {
        self.isForeground = isForeground
        self.renewInterval = renewInterval
        self.sleep = sleep
        self.client = client
        self.transfersRunning = transfersRunning
    }

    /// After connecting: the holder given by `GET /device`, then renewals
    /// (the first one at once) if the app is in the foreground. A lease
    /// held by another client is never taken.
    public func start(device: DeviceStatus, sentAt: ContinuousClock.Instant = .now) {
        loop?.cancel()
        loop = nil
        stopped = false
        stateSentAt = sentAt
        if !device.hasWriterField {
            // A pak without M10 may still have the route: the first
            // renewal tells (`404` marks it unsupported).
            access = .unknown
            expiresAt = nil
        } else {
            apply(device.writer)
        }
        if wantsLease { startLoop(immediate: true) }
    }

    /// The connection is gone: renewals stop (the state is kept for display).
    public func stop() {
        stopped = true
        loop?.cancel()
        loop = nil
    }

    /// The app comes to the foreground or goes to the background. In the
    /// background with no transfer running, the lease is released (after
    /// every acquisition already sent).
    public func setForeground(_ foreground: Bool) async {
        isForeground = foreground
        guard !stopped else { return }
        if foreground {
            startLoop(immediate: true)
        } else if !transfersRunning() {
            loop?.cancel()
            loop = nil
            await release()
        }
    }

    /// A write was refused with `423 read_only`: another client holds the
    /// lease. The renewals go on (they take the lease when it is free).
    /// `sentAt`: when the refused request was sent; older than the request
    /// the current state comes from, it changes nothing.
    public func noteReadOnly(holder: String?, sentAt: ContinuousClock.Instant = .now) {
        guard sentAt >= stateSentAt else { return }
        stateSentAt = sentAt
        becomeReadOnly(holder: holder)
    }

    /// "Take over", after the user confirmed: `PUT /writer` with `force`.
    public func takeOver() async throws {
        guard client() != nil else { throw APIError.notPaired }
        loop?.cancel()
        loop = nil
        let outcome = await acquire(force: true)
        if wantsLease { startLoop(immediate: false) }
        if case .failure(let error) = outcome { throw error }
    }

    /// One renewal now (`PUT /writer` without `force`). Returns false when
    /// renewals should stop (fatal error, pak without M10).
    @discardableResult
    public func renew() async -> Bool {
        guard client() != nil else { return true }
        switch await acquire(force: false) {
        case .success:
            return true
        case .failure(let error as APIError):
            if error.isReadOnly { return true }
            if Self.meansUnsupported(error) || Self.isFatal(error) { return false }
            return true
        case .failure:
            return true
        }
    }

    /// `DELETE /writer`, once every lease request sent before it has been
    /// answered, if this iPhone holds the lease or may hold it (an
    /// acquisition since the last release). Best effort; no effect on the
    /// pak when another client holds it.
    public func release() async {
        await serialized { [weak self] in
            guard let self, let client = self.client() else { return }
            guard self.access == .writer || self.acquisitionsSinceRelease > 0 else { return }
            self.acquisitionsSinceRelease = 0
            let sentAt = ContinuousClock.now
            if sentAt >= self.stateSentAt {
                self.stateSentAt = sentAt
                if self.access == .writer { self.access = .free }
                self.expiresAt = nil
            }
            try? await client.releaseWriter()
        }
    }

    // MARK: - Private

    /// `PUT /writer` through the serial queue; its answer is applied there
    /// (unless a newer request already set the state).
    private func acquire(force: Bool) async -> Result<Void, any Error> {
        await serialized { [weak self] () -> Result<Void, any Error> in
            guard let self, let client = self.client() else { return .failure(APIError.notPaired) }
            self.acquisitionsSinceRelease += 1
            let sentAt = ContinuousClock.now
            do {
                let state = try await client.takeWriter(force: force)
                if sentAt >= self.stateSentAt {
                    self.stateSentAt = sentAt
                    self.apply(state.writer)
                }
                return .success(())
            } catch let error as APIError {
                guard sentAt >= self.stateSentAt else { return .failure(error) }
                if error.isReadOnly {
                    self.stateSentAt = sentAt
                    self.becomeReadOnly(holder: error.readOnlyWriter)
                } else if Self.meansUnsupported(error) {
                    self.stateSentAt = sentAt
                    self.access = .unsupported
                    self.expiresAt = nil
                } else if Self.isFatal(error) {
                    self.onFatalError?(error)
                } else if case .network = error {
                    self.onConnectionFailure?(error)
                }
                // A lost connection: try again at the next renewal.
                return .failure(error)
            } catch {
                return .failure(error)
            }
        }
    }

    /// Runs `operation` after every lease request queued before it. It is
    /// never cancelled: a caller that stops waiting leaves it to finish.
    private func serialized<T: Sendable>(_ operation: @escaping @MainActor () async -> T) async -> T {
        let previous = queueTail
        let task = Task { @MainActor in
            await previous?.value
            return await operation()
        }
        queueTail = Task { @MainActor in _ = await task.value }
        return await task.value
    }

    private func startLoop(immediate: Bool) {
        loop?.cancel()
        let interval = renewInterval
        let sleep = sleep
        loop = Task { [weak self] in
            var wait = !immediate
            while !Task.isCancelled {
                if wait {
                    do { try await sleep(interval) } catch { return }
                    if Task.isCancelled { return }
                }
                wait = true
                guard let self else { return }
                guard await self.step() else {
                    if !Task.isCancelled { self.loop = nil }
                    return
                }
            }
        }
    }

    /// One turn of the loop: renew while needed, else release and stop.
    private func step() async -> Bool {
        if wantsLease {
            return await renew()
        }
        await release()
        return false
    }

    private func apply(_ writer: Writer?) {
        guard let writer else {
            access = .free
            expiresAt = nil
            return
        }
        if writer.you {
            let wasWriter = access == .writer
            access = .writer
            expiresAt = writer.expiresAt
            if !wasWriter { onAcquired?() }
        } else {
            becomeReadOnly(holder: writer.clientName)
        }
    }

    private func becomeReadOnly(holder: String?) {
        let tookOver: Bool
        switch access {
        case .writer: tookOver = true
        case .readOnly(_, let previous): tookOver = previous
        default: tookOver = false
        }
        let name = holder ?? access.holder ?? ""
        access = .readOnly(holder: name, tookOver: tookOver)
        expiresAt = nil
    }

    /// A pak without the M10 routes.
    static func meansUnsupported(_ error: APIError) -> Bool {
        switch error {
        case .server(let server): server.code == .notFound && server.status == 404
        case .unexpectedStatus(let status): status == 404 || status == 405
        default: false
        }
    }

    /// Errors after which renewing makes no sense.
    static func isFatal(_ error: APIError) -> Bool {
        switch error.code {
        case .disconnected, .unknownClient: true
        default: error == .notPaired
        }
    }
}
