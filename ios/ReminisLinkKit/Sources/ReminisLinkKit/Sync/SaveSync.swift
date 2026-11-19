import Foundation

/// What the sync needs from a console (``APIClient`` in the app, a stub in tests).
public protocol SaveSyncSource: Sendable {
    func saves() async throws -> SaveList
    func systems() async throws -> [GameSystem]
    func downloadSave(gameId: String, into directory: URL) async throws -> DownloadedFile
    func downloadSaveClock(gameId: String, into directory: URL) async throws -> DownloadedFile
}

extension APIClient: SaveSyncSource {}

/// Where a sync stands.
public struct SaveSyncProgress: Sendable, Equatable {
    /// Saves listed by the console.
    public var total: Int
    /// Saves compared so far.
    public var checked: Int
    /// New versions stored so far.
    public var stored: Int
    /// The game being compared or downloaded.
    public var currentGame: String?

    public init(total: Int = 0, checked: Int = 0, stored: Int = 0, currentGame: String? = nil) {
        self.total = total
        self.checked = checked
        self.stored = stored
        self.currentGame = currentGame
    }

    public var fraction: Double { total == 0 ? 0 : Double(checked) / Double(total) }
}

/// Why the save of one game was not stored.
public enum SaveSyncError: Error, Equatable, Sendable {
    /// The file downloaded is not the one listed (`file`: its name): it
    /// changed on the console during the sync, or arrived damaged. No
    /// version is stored; the next sync tries again.
    case checksumMismatch(file: String)
}

/// A game whose save could not be copied (the others go on).
public struct SaveSyncFailure: Sendable, Equatable, Identifiable {
    public var id: String { gameId }
    public let gameId: String
    public let gameName: String
    public let error: SyncErrorBox

    public init(gameId: String, gameName: String, error: any Error) {
        self.gameId = gameId
        self.gameName = gameName
        self.error = SyncErrorBox(error)
    }
}

/// An error, compared by description (for `Equatable` reports).
public struct SyncErrorBox: Sendable, Equatable {
    public let error: any Error
    public init(_ error: any Error) { self.error = error }
    public static func == (a: SyncErrorBox, b: SyncErrorBox) -> Bool {
        String(describing: a.error) == String(describing: b.error)
    }
}

/// The outcome of a sync that went to its end.
public struct SaveSyncReport: Sendable, Equatable {
    public let location: SaveVaultLocation
    /// Saves listed by the console.
    public let total: Int
    /// Games with a new version.
    public let stored: [String]
    /// Saves identical to their newest version.
    public let unchanged: Int
    public let failures: [SaveSyncFailure]
    /// The console listed only its first 10000 saves.
    public let truncated: Bool
    public let finishedAt: Date
}

/// Copies the saves of a console that changed since their newest version
/// (milestone M9): `GET /saves`, then for each save whose SHA-256 (or the
/// SHA-256 of its `.rtc`) differs, `GET /games/{gameId}/save` (and
/// `/save/rtc`), checked against the listed SHA-256, stored as a new
/// version (the vault rotates).
///
/// A failure on one game is recorded and the sync goes on; losing the
/// console (network error, `401 unknown_client`) or a cancellation stops
/// it (versions already stored stay).
public struct SaveSync: Sendable {
    public let console: VaultConsoleInfo
    public let source: any SaveSyncSource
    public let vault: any SaveVault
    /// Where downloads wait before going into the vault (emptied after each game).
    public let scratchDirectory: URL

    public init(console: VaultConsoleInfo, source: any SaveSyncSource, vault: any SaveVault, scratchDirectory: URL) {
        self.console = console
        self.source = source
        self.vault = vault
        self.scratchDirectory = scratchDirectory
    }

    public func run(progress: @escaping @Sendable (SaveSyncProgress) -> Void = { _ in }) async throws -> SaveSyncReport {
        let list = try await source.saves()
        try Task.checkCancellation()
        // Rotations left unfinished (the app stopped between a move and
        // its deletions), also for games no longer listed. Housekeeping:
        // a failure here does not stop the sync.
        try? await vault.finishRotations(deviceId: console.deviceId)
        // Folder names of the systems: label and tag, as two systems can
        // share a label (GBA and MGBA are both "Game Boy Advance"). Without
        // the list (a busy pak), the tag of the saves folder (`Saves/<TAG>/...`).
        let labels = Dictionary(
            (try? await source.systems())?.map { ($0.id, Self.systemName($0)) } ?? [], uniquingKeysWith: { a, _ in a }
        )

        var state = SaveSyncProgress(total: list.saves.count)
        progress(state)
        var stored: [String] = []
        var unchanged = 0
        var failures: [SaveSyncFailure] = []
        for save in list.saves {
            try Task.checkCancellation()
            state.currentGame = save.gameName
            progress(state)
            do {
                if try await sync(save, systemName: labels[save.systemId] ?? Self.systemName(fromPath: save.path)) {
                    stored.append(save.gameId)
                    state.stored += 1
                } else {
                    unchanged += 1
                }
            } catch {
                if Self.stopsSync(error) { throw error }
                failures.append(SaveSyncFailure(gameId: save.gameId, gameName: save.gameName, error: error))
            }
            state.checked += 1
            state.currentGame = nil
            progress(state)
        }
        return SaveSyncReport(
            location: vault.location, total: list.saves.count, stored: stored, unchanged: unchanged,
            failures: failures, truncated: list.truncated, finishedAt: Date()
        )
    }

    /// Whether a save needs a new version: its SHA-256 or the SHA-256 of
    /// its clock file differ from the newest version (or there is none).
    public static func needsNewVersion(_ save: SaveSummary, latest: SaveVersion?) -> Bool {
        guard let latest else { return true }
        return latest.info.save.sha256 != save.sha256.lowercased()
            || latest.info.rtc?.sha256 != save.rtc?.sha256.lowercased()
    }

    /// Stores a new version if the save changed; returns whether it did.
    private func sync(_ save: SaveSummary, systemName: String) async throws -> Bool {
        let latest = try await vault.latestVersion(deviceId: console.deviceId, gameId: save.gameId)
        guard Self.needsNewVersion(save, latest: latest) else { return false }

        let folder = scratchDirectory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }

        let saveFile = try await source.downloadSave(gameId: save.gameId, into: folder)
        let saveSHA = try await Self.checked(saveFile.url, expected: save.sha256)
        var rtcFile: NewVersionFile?
        if let rtc = save.rtc {
            do {
                let file = try await source.downloadSaveClock(gameId: save.gameId, into: folder)
                rtcFile = NewVersionFile(url: file.url, name: file.url.lastPathComponent, sha256: try await Self.checked(file.url, expected: rtc.sha256))
            } catch APIError.server(let error) where error.status == 404 {
                // Removed since the listing: the save alone.
            }
        }
        _ = try await vault.addVersion(
            console: console,
            game: VaultGameInfo(gameId: save.gameId, systemId: save.systemId, systemName: systemName, name: save.gameName),
            save: NewVersionFile(url: saveFile.url, name: saveFile.url.lastPathComponent, sha256: saveSHA),
            rtc: rtcFile, modifiedAt: save.modifiedAt
        )
        return true
    }

    /// The SHA-256 of a downloaded file, which must be the listed one.
    private static func checked(_ url: URL, expected: String) async throws -> String {
        let sha = try await FileDigest.sha256(of: url)
        guard sha == expected.lowercased() else { throw SaveSyncError.checksumMismatch(file: url.lastPathComponent) }
        return sha
    }

    /// Errors that end the whole sync rather than one game.
    public static func stopsSync(_ error: any Error) -> Bool {
        if error is CancellationError { return true }
        guard let api = error as? APIError else { return false }
        if case .network = api { return true }
        if case .notPaired = api { return true }
        return api.code == .unknownClient || api.code == .shuttingDown
    }

    /// `Game Boy Advance (GBA)`.
    public static func systemName(_ system: GameSystem) -> String {
        system.tag.isEmpty ? system.label : "\(system.label) (\(system.tag))"
    }

    /// `GBA` for `Saves/GBA/Game.gba.sav`.
    static func systemName(fromPath path: String) -> String {
        let parts = path.split(separator: "/")
        return parts.count >= 3 ? String(parts[parts.count - 2]) : "_"
    }
}
