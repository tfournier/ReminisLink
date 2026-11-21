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
    /// A sync asked for while one runs (a new connection): it starts when
    /// the running one ends, with the connection that asked. Independent
    /// of the running task: cancelling that task drops only the requests
    /// made before the cancellation, never one made after it.
    @ObservationIgnored fileprivate var pendingRequest: UUID?

    var isRunning: Bool {
        if case .running = phase { return true }
        return false
    }

    var progress: SaveSyncProgress? {
        if case .running(let progress) = phase { return progress }
        return nil
    }

    /// Stops the running sync, and the sync a connection asked for so far.
    func cancel() {
        pendingRequest = nil
        task?.cancel()
    }

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
    /// Stops every save sync and returns their tasks (to wait for them).
    /// Stops the save sync of a console (M15: its pairing was forgotten,
    /// paused or replaced).
    func cancelSaveSync(deviceId: String) {
        syncStates[deviceId]?.cancel()
    }

    func cancelSaveSyncs() -> [Task<Void, Never>] {
        let tasks = syncStates.values.compactMap(\.task)
        for state in syncStates.values { state.cancel() }
        return tasks
    }

    /// The demo mode opens (M16): the real app stops as if it went to the
    /// background (Bonjour, uploads after their current chunk) and its
    /// running save syncs stop; they start again in ``resumeAfterDemo()``.
    func pauseForDemo() {
        isPausedForDemo = true
        discovery.stop()
        enterBackground()
        syncsPausedForDemo = syncStates.filter { $0.value.task != nil }.map(\.key).sorted()
        _ = cancelSaveSyncs()
    }

    /// The demo mode ended: Bonjour, uploads and the stopped syncs go on.
    func resumeAfterDemo() {
        isPausedForDemo = false
        discovery.start()
        becomeActive()
        let paused = syncsPausedForDemo
        syncsPausedForDemo = []
        for deviceId in paused { startSaveSync(deviceId: deviceId, afterCurrent: true) }
    }

    func saveSync(for deviceId: String) -> SaveSyncState {
        if let state = syncStates[deviceId] { return state }
        let state = SaveSyncState()
        syncStates[deviceId] = state
        return state
    }

    /// Starts the save sync of a console, unless one is running for it
    /// (never two at once for the same console, even across connections).
    /// With `afterCurrent` (a new connection), a running sync is followed
    /// by a new one, with the new client, once it ends.
    func startSaveSync(deviceId: String, afterCurrent: Bool = false) {
        guard !isShutDown else { return }
        // M16: asked while the demo runs (a connection that ended then):
        // run when the demo ends.
        if isPausedForDemo {
            if !syncsPausedForDemo.contains(deviceId) { syncsPausedForDemo.append(deviceId) }
            return
        }
        let state = saveSync(for: deviceId)
        guard state.task == nil else {
            if afterCurrent { state.pendingRequest = UUID() }
            return
        }
        state.start()
        let token = background.begin(name: "Save sync") { [weak state] in state?.cancel() }
        state.task = Task { [weak self] in
            defer {
                state.task = nil
                self?.background.end(token)
                // Whether this task was cancelled or not: a request made
                // after its cancellation belongs to a new connection.
                if state.pendingRequest != nil {
                    state.pendingRequest = nil
                    self?.startSaveSync(deviceId: deviceId)
                }
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
        // Shut down or paused before this task first ran: nothing starts.
        try Task.checkCancellation()
        guard !isShutDown, !isPausedForDemo else { throw CancellationError() }
        let session = session(for: deviceId)
        let client = try await session.connectedClient()
        let vault = await vaultProvider.resolve()
        vaultLocation = vault.location
        let name = store.device(id: deviceId)?.name ?? session.hello?.deviceName ?? deviceId
        let sync = SaveSync(
            console: VaultConsoleInfo(deviceId: deviceId, name: name), source: SessionSaveSource(session: session, client: client),
            vault: vault,
            scratchDirectory: syncScratchDirectory
        )
        do {
            return try await sync.run { progress in
                Task { @MainActor in state.update(progress) }
            }
        } catch {
            let appError = AppError(error)
            if ConsoleSession.losesConnection(appError) { session.connectionLost(appError, client: client) }
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

/// The client of a save sync, used only while it is still the session's
/// client (M15): once the session dropped it (pairing forgotten, paused or
/// replaced), the next request of the sync is not sent.
struct SessionSaveSource: SaveSyncSource, StateSyncSource {
    let session: ConsoleSession
    let client: APIClient

    private func current() async throws -> APIClient {
        guard await session.client === client else { throw CancellationError() }
        return client
    }

    func saves() async throws -> SaveList { try await current().saves() }
    func systems() async throws -> [GameSystem] { try await current().systems() }
    func downloadSave(gameId: String, into directory: URL) async throws -> DownloadedFile {
        try await current().downloadSave(gameId: gameId, into: directory)
    }
    func downloadSaveClock(gameId: String, into directory: URL) async throws -> DownloadedFile {
        try await current().downloadSaveClock(gameId: gameId, into: directory)
    }
    // The states (M19), with the same client rule.
    func allStates() async throws -> StateList { try await current().allStates() }
    func downloadState(gameId: String, slot: Int, into directory: URL) async throws -> DownloadedFile {
        try await current().downloadState(gameId: gameId, slot: slot, into: directory)
    }
    func downloadStateThumbnail(gameId: String, slot: Int, into directory: URL) async throws -> DownloadedFile {
        try await current().downloadStateThumbnail(gameId: gameId, slot: slot, into: directory)
    }
}

extension ConsoleSession {
    /// Restores a synced version of a save to the console (milestone M9):
    /// the version is read from `vault` (the one it was shown from) and
    /// checked against its SHA-256 before anything is sent, then sent with
    /// `PUT /games/{gameId}/save` (the queue above 64 MiB); the pak copies
    /// the current save first. Only the save is sent: the console keeps
    /// its clock file.
    func restore(_ version: SaveVersion, gameId: String, gameName: String, from vault: any SaveVault) async throws -> SaveReplacement {
        let files = try await app.work.run {
            try await vault.exportVerified(deviceId: deviceId, gameId: gameId, version: version, to: app.exportDirectory)
        }
        if let rtc = files.rtc { try? FileManager.default.removeItem(at: rtc) }
        let size: Int64
        do {
            size = try FileDigest.size(of: files.save)
        } catch {
            try? FileManager.default.removeItem(at: files.directory)
            throw error
        }
        let picked = PickedFile(url: files.save, name: version.info.save.name, sizeBytes: size)
        return try await replaceSave(gameId: gameId, gameName: gameName, with: picked)
    }
}
