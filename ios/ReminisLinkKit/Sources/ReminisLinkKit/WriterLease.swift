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
/// `423 read_only` answers seen elsewhere (any write of the app) come in
/// through ``noteReadOnly(holder:)``.
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

    /// Called when this iPhone gets the lease back after being read-only
    /// (paused uploads resume).
    @ObservationIgnored public var onAcquired: (() -> Void)?
    /// Called when a lease request meets an error that ends the session
    /// (`401 disconnected`, `401 unknown_client`...). The loop stops.
    @ObservationIgnored public var onFatalError: ((APIError) -> Void)?

    @ObservationIgnored private let renewInterval: Duration
    @ObservationIgnored private let sleep: @Sendable (Duration) async throws -> Void
    @ObservationIgnored private let client: () -> APIClient?
    @ObservationIgnored private let transfersRunning: () -> Bool
    @ObservationIgnored private var loop: Task<Void, Never>?
    /// Changes whenever the state is set by something other than a
    /// renewal: an older renewal answer is then dropped.
    @ObservationIgnored private var epoch = 0
    @ObservationIgnored private var stopped = false

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
    public func start(device: DeviceStatus) {
        loop?.cancel()
        loop = nil
        stopped = false
        epoch += 1
        if !device.hasWriterField {
            // A pak without M10 may still have the route: the first
            // renewal tells (`404` marks it unsupported).
            access = .unknown
        } else {
            apply(device.writer)
        }
        if isForeground || transfersRunning() { startLoop(immediate: true) }
    }

    /// The connection is gone: renewals stop (the state is kept for display).
    public func stop() {
        stopped = true
        loop?.cancel()
        loop = nil
    }

    /// The app comes to the foreground or goes to the background. In the
    /// background with no transfer running, the lease is released.
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
    public func noteReadOnly(holder: String?) {
        epoch += 1
        becomeReadOnly(holder: holder)
    }

    /// "Take over", after the user confirmed: `PUT /writer` with `force`.
    public func takeOver() async throws {
        guard let client = client() else { throw APIError.notPaired }
        epoch += 1
        let current = epoch
        loop?.cancel()
        loop = nil
        do {
            let state = try await client.takeWriter(force: true)
            if current == epoch { apply(state.writer) }
        } catch {
            if !stopped, isForeground || transfersRunning() { startLoop(immediate: false) }
            throw error
        }
        if !stopped, isForeground || transfersRunning() { startLoop(immediate: false) }
    }

    /// One renewal now (`PUT /writer` without `force`). Returns false when
    /// renewals should stop (fatal error, pak without M10).
    @discardableResult
    public func renew() async -> Bool {
        guard let client = client() else { return true }
        let current = epoch
        do {
            let state = try await client.takeWriter(force: false)
            guard current == epoch, !Task.isCancelled else { return true }
            apply(state.writer)
        } catch let error as APIError {
            guard current == epoch, !Task.isCancelled else { return true }
            if error.isReadOnly {
                becomeReadOnly(holder: error.readOnlyWriter)
                return true
            }
            if Self.meansUnsupported(error) {
                access = .unsupported
                expiresAt = nil
                return false
            }
            if Self.isFatal(error) {
                onFatalError?(error)
                return false
            }
            // A lost connection: try again at the next renewal.
        } catch {
        }
        return true
    }

    /// `DELETE /writer` if this iPhone holds the lease (best effort).
    public func release() async {
        guard access == .writer, let client = client() else { return }
        epoch += 1
        access = .free
        expiresAt = nil
        try? await client.releaseWriter()
    }

    // MARK: - Private

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
        if isForeground || transfersRunning() {
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
            let wasReadOnly = access.isReadOnly
            access = .writer
            expiresAt = writer.expiresAt
            if wasReadOnly { onAcquired?() }
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
