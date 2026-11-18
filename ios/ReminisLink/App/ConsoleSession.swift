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

    @ObservationIgnored private unowned let app: AppModel
    /// The connection in progress, shared by every caller.
    @ObservationIgnored private var connectTask: Task<Void, Never>?

    init(deviceId: String, app: AppModel) {
        self.deviceId = deviceId
        self.app = app
    }

    var error: AppError? {
        if case .failed(let error) = state { return error }
        return nil
    }

    func connectIfNeeded() async {
        if state == .idle || connectTask != nil { await connect() }
    }

    /// Finds the console, checks it with `/hello` (which also measures the
    /// clock offset), then loads the dashboard. A caller arriving while a
    /// connection is in progress waits for it. Cancelling the caller
    /// cancels the connection, which goes back to `.idle` (never cached as
    /// a failure).
    func connect() async {
        let task: Task<Void, Never>
        if let connectTask {
            task = connectTask
        } else {
            state = .connecting
            task = Task { await self.runConnection() }
            connectTask = task
        }
        await withTaskCancellationHandler {
            await task.value
        } onCancel: {
            task.cancel()
        }
    }

    private func runConnection() async {
        defer { connectTask = nil }
        do {
            guard let credentials = try app.store.credentials(for: deviceId) else {
                throw AppError.api(.notPaired)
            }
            let endpoint = try await app.endpoint(for: deviceId)
            let client = app.makeClient(endpoint: endpoint, expectedDeviceId: deviceId, credentials: credentials)
            hello = try await client.hello()
            status = try await client.device()
            try Task.checkCancellation()
            self.client = client
            state = .connected
            await loadTrash()
        } catch {
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

    /// The trash route comes with milestone M4 on the pak: a failure here
    /// only hides the trash, it does not fail the dashboard.
    func loadTrash() async {
        trash = try? await client?.trash()
    }

    /// Runs a request, connecting first if needed (or waiting for the
    /// connection in progress). A failure also marks the session failed
    /// when the console is gone or no longer knows this iPhone.
    func perform<T: Sendable>(_ operation: @Sendable (APIClient) async throws -> T) async throws -> T {
        if client == nil { await connect() }
        try Task.checkCancellation()
        guard let client else { throw error ?? AppError.consoleNotFound }
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

    func emptyTrash() async throws {
        _ = try await perform { try await $0.emptyTrash() }
        await loadTrash()
    }

    func deleteTrashEntry(_ id: String) async throws {
        _ = try await perform { try await $0.deleteTrashEntry(id: id) }
        await loadTrash()
    }

    /// `DELETE /clients/me`, then forgets the key. Without a usable key,
    /// or if the console no longer knows this iPhone, the entry is only
    /// forgotten on this iPhone.
    func unpair() async throws {
        let credentials = try? app.store.credentials(for: deviceId)
        if credentials != nil {
            do {
                try await perform { try await $0.unpair() }
            } catch AppError.api(let error) where error.code == .unknownClient {
            }
        }
        try app.forget(deviceId: deviceId)
    }
}
