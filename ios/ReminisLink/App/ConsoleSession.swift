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
    func connect() async {
        let attempt: ConnectionAttempt
        if let current = self.attempt, !current.isCancelled {
            attempt = current
        } else {
            attempt = ConnectionAttempt()
            self.attempt = attempt
            state = .connecting
            attempt.task = Task { await self.runConnection(attempt) }
        }
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
            let client = app.makeClient(endpoint: endpoint, expectedDeviceId: deviceId, credentials: credentials)
            hello = try await client.hello()
            status = try await client.device()
            try Task.checkCancellation()
            guard self.attempt === attempt else { return }
            self.client = client
            state = .connected
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
            if Self.losesConnection(appError) {
                state = .failed(appError)
                self.client = nil
            }
            throw appError
        }
    }

    static func losesConnection(_ error: AppError) -> Bool {
        guard case .api(let api) = error else { return false }
        if case .network = api { return true }
        return api.code == .unknownClient
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

/// One connection, awaited by one or more callers.
@MainActor
private final class ConnectionAttempt {
    var task: Task<Void, Never>?
    var waiters: [UUID: CheckedContinuation<Void, Never>] = [:]
    private(set) var isFinished = false

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
