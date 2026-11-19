import Foundation
import Observation
import ReminisLinkKit

/// The connection to one paired console while its screens are open.
@MainActor
@Observable
final class ConsoleSession {
    enum State: Equatable {
        case idle
        case connecting
        case connected
        case failed(AppError)
    }

    let deviceId: String
    private(set) var state: State = .idle
    private(set) var hello: Hello?
    private(set) var status: DeviceStatus?
    private(set) var trash: TrashList?
    private(set) var client: APIClient?
    /// Changes when games were added or deleted: the library screens reload.
    private(set) var libraryRevision = 0
    /// Changes when a game art was set or removed: art views load again.
    var artRevision = 0

    /// Uploads and jobs of this console.
    @ObservationIgnored private(set) lazy var transfers: TransferQueue = makeTransfers()
    /// The write lease of this iPhone on the console (milestone M10).
    @ObservationIgnored private(set) lazy var lease: WriterLease = makeLease()
    /// Waits for the write access to come back (cheat import, patch upload).
    @ObservationIgnored private var writeAccessWaiters: [UUID: CheckedContinuation<Void, Error>] = [:]
    /// Something waits for the write access to come back (shown as paused).
    private(set) var writeAccessWaits = 0

    @ObservationIgnored unowned let app: AppModel
    /// The connection in progress, shared by every caller.
    @ObservationIgnored private var attempt: ConnectionAttempt?

    init(deviceId: String, app: AppModel) {
        self.deviceId = deviceId
        self.app = app
    }

    var error: AppError? {
        if case .failed(let error) = state { return error }
        return nil
    }

    /// Another client has write access (M10): write actions are disabled.
    var isReadOnly: Bool { lease.isReadOnly }

    /// The console disconnected this iPhone (`401 disconnected`).
    var isDisconnected: Bool { error?.isDisconnected ?? false }

    /// The user opened this console from the list of consoles: the
    /// connection that starts now (or is in progress) may lift a
    /// disconnection set from the console. Only that attempt: a later one
    /// (automatic) never may.
    func userOpened() {
        switch state {
        case .idle, .connecting:
            _ = currentAttempt(explicit: true)
        case .failed(let error) where error.isDisconnected:
            _ = currentAttempt(explicit: true)
        default:
            break
        }
    }

    /// "Reconnect", after a disconnection by the console: connects again,
    /// lifting the disconnection (`POST /clients/me/connect`). The
    /// permission belongs to this attempt only, whatever its outcome.
    func reconnect() async {
        await connect(explicit: true)
    }

    func connectIfNeeded() async {
        if state == .idle || attempt != nil { await connect() }
    }

    /// Finds the console, checks it with `/hello` (which also measures the
    /// clock offset), then loads the dashboard.
    ///
    /// Callers arriving while a connection is in progress wait for it. A
    /// cancelled caller stops waiting at once; the connection itself is
    /// only cancelled when its last caller is gone, and then goes back to
    /// `.idle` (never cached as a failure). A cancelled connection is never
    /// joined: a new one starts.
    ///
    /// `explicit`: an action of the user (M10, "Reconnect" or opening the
    /// console from the list): this attempt may lift a disconnection.
    func connect(explicit: Bool = false) async {
        let attempt = currentAttempt(explicit: explicit)
        let waiter = UUID()
        await withTaskCancellationHandler {
            await withCheckedContinuation { (continuation: CheckedContinuation<Void, Never>) in
                if attempt.isFinished || Task.isCancelled {
                    continuation.resume()
                } else {
                    attempt.waiters[waiter] = continuation
                }
            }
        } onCancel: {
            Task { @MainActor in attempt.leave(waiter) }
        }
        if Task.isCancelled { attempt.leave(waiter) }
    }

    /// The connection in progress, or a new one.
    private func currentAttempt(explicit: Bool) -> ConnectionAttempt {
        let attempt: ConnectionAttempt
        if let current = self.attempt, !current.isCancelled {
            attempt = current
        } else {
            attempt = ConnectionAttempt()
            self.attempt = attempt
            state = .connecting
            attempt.task = Task { await self.runConnection(attempt) }
        }
        if explicit { attempt.isExplicit = true }
        return attempt
    }

    private func runConnection(_ attempt: ConnectionAttempt) async {
        defer {
            if self.attempt === attempt { self.attempt = nil }
            attempt.finish()
        }
        do {
            guard let credentials = try app.store.credentials(for: deviceId) else {
                throw AppError.api(.notPaired)
            }
            let endpoint = try await app.endpoint(for: deviceId)
            let observed = ObservedClient()
            let client = app.makeClient(
                endpoint: endpoint, expectedDeviceId: deviceId, credentials: credentials,
                errorObserver: { [weak self] error, sentAt in
                    Task { @MainActor in
                        guard let client = observed.client else { return }
                        self?.observe(error, sentAt: sentAt, from: client)
                    }
                }
            )
            observed.client = client
            hello = try await client.hello()
            let status: DeviceStatus
            var deviceSentAt = ContinuousClock.now
            do {
                status = try await client.device()
            } catch let error as APIError where error.isDisconnected && attempt.isExplicit {
                // Only after an explicit action of the user (M10).
                try await client.connectClient()
                deviceSentAt = .now
                status = try await client.device()
            }
            self.status = status
            try Task.checkCancellation()
            guard self.attempt === attempt else { return }
            self.client = client
            state = .connected
            lease.start(device: status, sentAt: deviceSentAt)
            // Milestone M9: copy the saves that changed (setting, default on).
            // A sync of the previous connection still running: this one
            // starts when it ends.
            if app.syncOnConnect { app.startSaveSync(deviceId: deviceId, afterCurrent: true) }
            await loadTrash()
        } catch {
            // A cancelled connection replaced by a newer one changes nothing.
            guard self.attempt === attempt else { return }
            let error = AppError(error)
            state = error.isCancellation || Task.isCancelled ? .idle : .failed(error)
        }
    }

    /// Reloads the dashboard without searching the console again.
    func refresh() async {
        guard let client else { return await connect() }
        do {
            status = try await client.device()
            state = .connected
            await loadTrash()
        } catch {
            let error = AppError(error)
            if !error.isCancellation { state = .failed(error) }
        }
    }

    /// Reloads the trash. A failure keeps the last known content (it never
    /// fails the dashboard). Returns whether the content is fresh.
    @discardableResult
    func loadTrash() async -> Bool {
        guard let client, let fresh = try? await client.trash() else { return false }
        trash = fresh
        return true
    }

    /// Runs a request, connecting first if needed (or waiting for the
    /// connection in progress). A failure also marks the session failed
    /// when the console is gone or no longer knows this iPhone.
    func perform<T: Sendable>(_ operation: @Sendable (APIClient) async throws -> T) async throws -> T {
        if client == nil { await connect() }
        try Task.checkCancellation()
        guard let client else { throw error ?? AppError.unexpected }
        do {
            return try await operation(client)
        } catch {
            let appError = AppError(error)
            if Self.losesConnection(appError) { connectionLost(appError, client: client) }
            throw appError
        }
    }

    /// The console is gone or no longer knows this iPhone, as seen by a
    /// request sent with `client`. A request of an older connection
    /// changes nothing: a reconnection has installed another client since.
    func connectionLost(_ error: AppError, client failed: APIClient) {
        guard let client, client === failed else { return }
        state = .failed(error)
        self.client = nil
        lease.stop()
    }

    static func losesConnection(_ error: AppError) -> Bool {
        guard case .api(let api) = error else { return false }
        if case .network = api { return true }
        return api.code == .unknownClient || api.code == .disconnected
    }

    /// Every error answer of the pak to this connection (M10): a
    /// `423 read_only` makes the session read-only, a `401 disconnected`
    /// ends the connection, whatever screen or transfer sent the request.
    func observe(_ error: ServerError, sentAt: ContinuousClock.Instant, from client: APIClient) {
        switch error.code {
        case .readOnly:
            guard self.client === client else { return }
            // A request sent before the last take-over changes nothing.
            lease.noteReadOnly(holder: error.writerName, sentAt: sentAt)
        case .disconnected:
            connectionLost(.api(.server(error)), client: client)
        default:
            break
        }
    }

    /// "Take over", after the user confirmed: this iPhone gets write
    /// access, the other client becomes read-only.
    func takeOver() async throws {
        _ = try await connectedClient()
        do {
            try await lease.takeOver()
        } catch {
            let appError = AppError(error)
            if let client, Self.losesConnection(appError) { connectionLost(appError, client: client) }
            throw appError
        }
    }

    /// The app comes to the foreground or goes to the background: the
    /// lease is renewed in the foreground (or while a transfer runs), and
    /// released in the background.
    func setForeground(_ foreground: Bool) {
        guard client != nil else { return }
        let lease = lease
        Task { await lease.setForeground(foreground) }
    }

    /// The lease is held again: paused uploads resume, waiters go on.
    private func writeAccessRegained() {
        transfers.resumePausedForWriteAccess()
        let waiters = writeAccessWaiters.values
        writeAccessWaiters.removeAll()
        for waiter in waiters { waiter.resume() }
    }

    /// Whether this iPhone holds the lease, confirmed by the pak (a
    /// renewal) when it believes so: a `423` older than the lease change
    /// must not leave an upload paused, nor resume one for nothing.
    func writeAccessConfirmed() async -> Bool {
        guard client != nil, lease.access == .writer else { return false }
        await lease.renew()
        return lease.access == .writer
    }

    /// Waits until this iPhone has the write access again (M10). Throws
    /// `CancellationError` if the caller is cancelled meanwhile.
    func waitForWriteAccess() async throws {
        if await writeAccessConfirmed() { return }
        let id = UUID()
        writeAccessWaits += 1
        defer { writeAccessWaits -= 1 }
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                if Task.isCancelled {
                    continuation.resume(throwing: CancellationError())
                } else {
                    writeAccessWaiters[id] = continuation
                }
            }
        } onCancel: {
            Task { @MainActor in
                self.writeAccessWaiters.removeValue(forKey: id)?.resume(throwing: CancellationError())
            }
        }
    }

    /// Runs `operation` (an upload that keeps its progress across calls);
    /// refused by `423 read_only` or `401 disconnected`, it waits for the
    /// write access to come back and runs again.
    func pausingForWriteAccess<T>(_ operation: () async throws -> T) async throws -> T {
        while true {
            do {
                return try await operation()
            } catch let error as APIError where error.isReadOnly || error.isDisconnected {
                try await waitForWriteAccess()
            } catch let error as AppError where error.isReadOnly || error.isDisconnected {
                try await waitForWriteAccess()
            }
        }
    }

    private func makeLease() -> WriterLease {
        let lease = WriterLease(
            isForeground: app.isForeground,
            client: { [weak self] in self?.client },
            transfersRunning: { [weak self] in self?.transfers.isBusy ?? false }
        )
        // The lease is back: uploads paused by `423` go on, and what waits
        // for it (cheat import, patch upload) resumes.
        lease.onAcquired = { [weak self] in self?.writeAccessRegained() }
        lease.onFatalError = { [weak self] error in
            guard let self, let client = self.client else { return }
            self.connectionLost(AppError(error), client: client)
        }
        return lease
    }

    /// A signed client, connecting first if needed.
    func connectedClient() async throws -> APIClient {
        if client == nil { await connect() }
        try Task.checkCancellation()
        guard let client else { throw error ?? AppError.unexpected }
        return client
    }

    private func makeTransfers() -> TransferQueue {
        let queue = TransferQueue(
            scratchDirectory: app.transferDirectory.appending(path: "chunks-\(deviceId)"),
            background: app.background,
            client: { [weak self] in
                guard let self else { throw CancellationError() }
                return try await self.connectedClient()
            }
        )
        queue.onUploadFinished = { [weak self] _ in self?.contentChanged() }
        queue.onJobFinished = { [weak self] item in
            if item.job?.state == .succeeded { self?.contentChanged() }
        }
        // M10: the lease may be back by the time the pause is recorded.
        queue.onPausedForWriteAccess = { [weak self, weak queue] item in
            Task { @MainActor in
                guard let self, await self.writeAccessConfirmed() else { return }
                queue?.resume(item)
            }
        }
        return queue
    }

    /// Games were added or deleted: reload the library and the dashboard.
    func contentChanged() {
        libraryRevision += 1
        Task { await refresh() }
    }

    /// `DELETE /games/{gameId}`: everything goes to the trash of the console.
    func deleteGame(id: String, keepSave: Bool) async throws -> TrashMoveResult {
        let result = try await perform { try await $0.deleteGame(id: id, keepSave: keepSave) }
        contentChanged()
        return result
    }

    func emptyTrash() async throws {
        _ = try await perform { try await $0.emptyTrash() }
        await refresh()
    }

    func deleteTrashEntry(_ id: String) async throws {
        _ = try await perform { try await $0.deleteTrashEntry(id: id) }
        await refresh()
    }

    /// `DELETE /clients/me`, then forgets the key. Without a key, or if the
    /// console no longer knows this iPhone, the entry is only forgotten on
    /// this iPhone. A Keychain read error is reported, not taken for "no key".
    func unpair() async throws {
        if try app.store.credentials(for: deviceId) != nil {
            do {
                try await perform { try await $0.unpair() }
            } catch AppError.api(let error) where error.code == .unknownClient {
            }
        }
        try app.forget(deviceId: deviceId)
    }
}

/// The client of a connection, for the error observer set before it exists.
/// Weak: the client holds the observer, which holds this.
private final class ObservedClient: @unchecked Sendable {
    private let lock = NSLock()
    private weak var _client: APIClient?
    var client: APIClient? {
        get { lock.withLock { _client } }
        set { lock.withLock { _client = newValue } }
    }
}

/// One connection, awaited by one or more callers.
@MainActor
private final class ConnectionAttempt {
    var task: Task<Void, Never>?
    var waiters: [UUID: CheckedContinuation<Void, Never>] = [:]
    private(set) var isFinished = false
    /// Started or joined by an explicit action of the user (M10): may lift
    /// a disconnection set from the console.
    var isExplicit = false

    var isCancelled: Bool { task?.isCancelled ?? false }

    /// A caller stops waiting; the last one cancels the connection.
    func leave(_ waiter: UUID) {
        waiters.removeValue(forKey: waiter)?.resume()
        if waiters.isEmpty && !isFinished { task?.cancel() }
    }

    func finish() {
        isFinished = true
        let continuations = waiters.values
        waiters.removeAll()
        for continuation in continuations { continuation.resume() }
    }
}
