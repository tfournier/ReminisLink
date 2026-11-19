import Foundation
import Observation
import ReminisLinkKit

/// Where synced saves go: the vault is chosen again at each use, so that
/// turning iCloud Drive on or off is followed (milestone M9).
struct SaveVaultProvider: Sendable {
    let resolve: @Sendable () async -> any SaveVault

    /// iCloud Drive (the container's Documents, "ReminisLink" in Files),
    /// else the app's Documents (Files > On My iPhone > ReminisLink).
    static func live(containerIdentifier: String?) -> SaveVaultProvider {
        let staging = URL.applicationSupportDirectory.appending(path: "SyncStaging", directoryHint: .isDirectory)
        // Versions left half assembled by a previous run.
        try? FileManager.default.removeItem(at: staging)
        return SaveVaultProvider {
            await SaveVaultLocator.resolve(
                containerIdentifier: containerIdentifier, localRoot: URL.documentsDirectory, stagingDirectory: staging
            )
        }
    }

    /// A plain folder (tests, demo).
    static func folder(_ root: URL, location: SaveVaultLocation) -> SaveVaultProvider {
        let vault = FileSaveVault(location: location, root: root, stagingDirectory: root.deletingLastPathComponent().appending(path: "SyncStaging-\(UUID().uuidString)"))
        return SaveVaultProvider { vault }
    }
}

/// The save sync of one console.
@MainActor
@Observable
final class SaveSyncState {
    enum Phase: Equatable {
        case idle
        case running(SaveSyncProgress)
        case finished(SaveSyncReport)
        case failed(AppError)
    }

    private(set) var phase: Phase = .idle
    /// The last sync that went to its end (kept while a new one runs).
    private(set) var lastReport: SaveSyncReport?
    @ObservationIgnored fileprivate var task: Task<Void, Never>?

    var isRunning: Bool {
        if case .running = phase { return true }
        return false
    }

    var progress: SaveSyncProgress? {
        if case .running(let progress) = phase { return progress }
        return nil
    }

    func cancel() { task?.cancel() }

    fileprivate func start() { phase = .running(SaveSyncProgress()) }

    /// Reports arrive in order but late: never after the end, never backwards.
    fileprivate func update(_ progress: SaveSyncProgress) {
        guard case .running(let current) = phase, progress.checked >= current.checked else { return }
        phase = .running(progress)
    }

    fileprivate func finish(_ result: Result<SaveSyncReport, AppError>) {
        switch result {
        case .success(let report):
            lastReport = report
            phase = .finished(report)
        case .failure(let error) where error.isCancellation:
            phase = lastReport.map { .finished($0) } ?? .idle
        case .failure(let error):
            phase = .failed(error)
        }
    }
}

extension AppModel {
    func saveSync(for deviceId: String) -> SaveSyncState {
        if let state = syncStates[deviceId] { return state }
        let state = SaveSyncState()
        syncStates[deviceId] = state
        return state
    }

    /// Starts the save sync of a console, unless one is running for it
    /// (never two at once for the same console, even across connections).
    func startSaveSync(deviceId: String) {
        let state = saveSync(for: deviceId)
        guard state.task == nil else { return }
        state.start()
        let token = background.begin(name: "Save sync") { [weak state] in state?.cancel() }
        state.task = Task { [weak self] in
            defer {
                state.task = nil
                self?.background.end(token)
            }
            guard let self else { return }
            do {
                let report = try await self.runSaveSync(deviceId: deviceId, state: state)
                state.finish(.success(report))
            } catch {
                state.finish(.failure(AppError.sync(error)))
            }
        }
    }

    private func runSaveSync(deviceId: String, state: SaveSyncState) async throws -> SaveSyncReport {
        let session = session(for: deviceId)
        let client = try await session.connectedClient()
        let vault = await vaultProvider.resolve()
        vaultLocation = vault.location
        let name = store.device(id: deviceId)?.name ?? session.hello?.deviceName ?? deviceId
        let sync = SaveSync(
            console: VaultConsoleInfo(deviceId: deviceId, name: name), source: client, vault: vault,
            scratchDirectory: syncScratchDirectory
        )
        do {
            return try await sync.run { progress in
                Task { @MainActor in state.update(progress) }
            }
        } catch {
            let appError = AppError(error)
            if ConsoleSession.losesConnection(appError) { session.connectionLost(appError) }
            throw error
        }
    }

    /// The vault to browse now (its location is shown).
    func currentVault() async -> any SaveVault {
        let vault = await vaultProvider.resolve()
        vaultLocation = vault.location
        return vault
    }
}
