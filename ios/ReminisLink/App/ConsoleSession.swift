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
    /// Set here and by the `device` events (M14).
    var status: DeviceStatus?
    private(set) var trash: TrashList?
    private(set) var client: APIClient?
    /// Changes when games were added or deleted: the library screens reload.
    /// Also changed by the `library` events (M14).
    var libraryRevision = 0
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
    /// The console closed ReminisLink (M11): no automatic connection until
    /// the user opens the console again.
    private(set) var closedByConsole = false

    @ObservationIgnored unowned let app: AppModel
    /// The connection in progress, shared by every caller.
    @ObservationIgnored private var attempt: ConnectionAttempt?
    /// The check in progress after the console's service disappeared (M11).
    @ObservationIgnored private var closureCheck: Task<Void, Never>?

    // Milestone M14 (ConsoleSession+Events.swift).
    /// The event stream of the connection, while connected and active.
    @ObservationIgnored var events: EventStream?
    /// The client the event stream belongs to.
    @ObservationIgnored weak var eventsClient: APIClient?
    /// `hello` events received in this session, every stream included:
    /// after the first, the screens reload (the pak replays nothing missed
    /// while no stream was open: background, another console shown).
    @ObservationIgnored var hellosSeen = 0
    /// The pak has no `GET /events` (older than M14): the screens poll,
    /// as before, for the rest of this session.
    var eventsUnsupported = false
    /// The event stream is open (`hello` received): jobs follow its events.
    var eventsLive = false
    /// The last `device` event and how many arrived: a `GET /device`
    /// answer older than the last event keeps the event's fields.
    @ObservationIgnored var lastDeviceEvent: EventDevice?
    @ObservationIgnored var deviceEvents = 0
    /// Library changes waiting for the debounce to end.
    @ObservationIgnored var pendingLibraryChange: LibraryChange?
    /// The systems the last change of ``libraryRevision`` touched, nil for
    /// all of them (an action of this app, or an event that says so).
    var lastLibraryChange: Set<String>?

    /// The app, for the event stream's callbacks (M14), which may come
    /// after the app is gone (tests): nil then, never a dangling `app`.
    @ObservationIgnored weak var liveApp: AppModel?

    init(deviceId: String, app: AppModel) {
        self.deviceId = deviceId
        self.app = app
        self.liveApp = app
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
        closedByConsole = false
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
        guard !closedByConsole else { return }
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
    ///
    /// After the console closed ReminisLink (M11), nothing connects but an
    /// explicit action: every automatic path (screens, transfers, a sheet
    /// that ends) comes through here and is refused.
    func connect(explicit: Bool = false) async {
        if closedByConsole {
            guard explicit else { return }
            closedByConsole = false
        }
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
            // M11: renamed on the console; `/hello` already checked the id.
            app.store.noteConsoleName(status.deviceName, for: deviceId)
            self.client = client
            state = .connected
            closedByConsole = false
            lease.start(device: status, sentAt: deviceSentAt)
            // M14: the console pushes its changes while the app is active.
            updateEventStream()
            // Uploads paused when the console closed ReminisLink go on (M11).
            transfers.resumeAfterReconnection()
            // Milestone M9: copy the saves that changed (setting, default on).
            // A sync of the previous connection still running: this one
            // starts when it ends.
            if app.syncOnConnect { app.startSaveSync(deviceId: deviceId, afterCurrent: true) }
            await loadTrash()
        } catch {
            // A cancelled connection replaced by a newer one changes nothing.
            guard self.attempt === attempt else { return }
            let error = AppError(error)
            // M11: the pak is quitting (`/hello` or `/device` got
            // `503 shutting_down`, or failed to connect once its Bonjour
            // announcement was withdrawn): back to the list, not an error
            // screen.
            if case .api(let api) = error, app.meansConsoleClosed(api, deviceId: deviceId) {
                return markClosedByConsole()
            }
            state = error.isCancellation || Task.isCancelled ? .idle : .failed(error)
        }
    }

    /// Reloads the dashboard without searching the console again.
    func refresh() async {
        guard let client else { return await connect() }
        do {
            let status = try await client.device()
            // M11: renamed on the console since the last answer.
            let renamed = self.status.map { $0.deviceName != status.deviceName } ?? false
            self.status = status
            state = .connected
            updateEventStream()
            if renamed { app.store.noteConsoleName(status.deviceName, for: deviceId) }
            await loadTrash()
        } catch {
            let error = AppError(error)
            if case .api(let api) = error, app.meansConsoleClosed(api, deviceId: deviceId) {
                return consoleClosed(client: client)
            }
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
        guard let client else { throw unavailableError }
        do {
            return try await operation(client)
        } catch {
            let appError = AppError(error)
            if case .api(let api) = appError, app.meansConsoleClosed(api, deviceId: deviceId) {
                consoleClosed(client: client)
            } else if Self.losesConnection(appError) {
                connectionLost(appError, client: client)
            }
            throw appError
        }
    }

    /// The console closed ReminisLink (M11), as seen by a request sent with
    /// `client`: the connection ends without an error screen, uploads pause
    /// (they go on at the next connection), and the app goes back to the
    /// list of consoles. A request of an older connection changes nothing.
    func consoleClosed(client failed: APIClient) {
        guard let client, client === failed else { return }
        self.client = nil
        lease.stop()
        stopEventStream()
        markClosedByConsole()
    }

    /// The console's Bonjour service disappeared, or a background request
    /// (a lease renewal) failed to connect (M11): sends `/hello` once, with
    /// a short timeout. `503 shutting_down` or no connection: the console
    /// closed ReminisLink (back to the list, uploads paused). An answer: a
    /// Bonjour glitch, the console stays open.
    ///
    /// `waitForBrowsing`: right after the browser started (back from the
    /// background), its results are not in yet: wait until it has browsed
    /// long enough, and probe only if the console is still not seen. A
    /// removal seen by the browser needs no wait.
    func checkStillOpen(waitForBrowsing: Bool) {
        guard let client, !closedByConsole, closureCheck == nil else { return }
        closureCheck = Task { [weak self] in
            await self?.runClosureCheck(client: client, waitForBrowsing: waitForBrowsing)
            self?.closureCheck = nil
        }
    }

    private func runClosureCheck(client: APIClient, waitForBrowsing: Bool) async {
        if waitForBrowsing {
            let deadline = ContinuousClock.now + app.closureMinimumBrowsing + .seconds(1)
            while !app.hasBrowsedLongEnough, ContinuousClock.now < deadline {
                try? await Task.sleep(for: .milliseconds(100))
            }
            // Stopped (background) or local network denied: nothing known.
            guard app.hasBrowsedLongEnough else { return }
        }
        guard self.client === client, !app.isServiceVisible(deviceId) else { return }
        do {
            try await client.hello(timeout: PakClosure.probeTimeout)
        } catch {
            if PakClosure.probeMeansClosed(error) { consoleClosed(client: client) }
        }
    }

    private func markClosedByConsole() {
        closedByConsole = true
        state = .idle
        transfers.pauseForClosedConsole()
        app.consoleClosed(deviceId: deviceId)
    }

    /// Why there is no client after a connection attempt.
    private var unavailableError: AppError {
        if let error { return error }
        return closedByConsole ? .consoleClosed : .unexpected
    }

    /// The console is gone or no longer knows this iPhone, as seen by a
    /// request sent with `client`. A request of an older connection
    /// changes nothing: a reconnection has installed another client since.
    func connectionLost(_ error: AppError, client failed: APIClient) {
        guard let client, client === failed else { return }
        state = .failed(error)
        self.client = nil
        lease.stop()
        stopEventStream()
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
        case .shuttingDown:
            // M11: whatever screen or transfer sent the request.
            consoleClosed(client: client)
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
        // M14: the event stream runs in the foreground only.
        updateEventStream()
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
        // M11: a renewal that cannot reach the console, whose Bonjour
        // service is gone: it closed ReminisLink.
        lease.onConnectionFailure = { [weak self] error in
            guard let self, let client = self.client else { return }
            if self.app.meansConsoleClosed(error, deviceId: self.deviceId) {
                self.consoleClosed(client: client)
            } else {
                // Maybe too early for Bonjour to tell (the browser has just
                // started): checked once it has browsed long enough.
                self.checkStillOpen(waitForBrowsing: true)
            }
        }
        return lease
    }

    /// A signed client, connecting first if needed.
    func connectedClient() async throws -> APIClient {
        if client == nil { await connect() }
        try Task.checkCancellation()
        guard let client else { throw unavailableError }
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
        lastLibraryChange = nil
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
