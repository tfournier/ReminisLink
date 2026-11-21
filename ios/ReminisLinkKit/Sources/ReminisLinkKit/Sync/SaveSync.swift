import Foundation

/// What the sync needs from a console (``APIClient`` in the app, a stub in tests).
public protocol SaveSyncSource: Sendable {
    func saves() async throws -> SaveList
    func systems() async throws -> [GameSystem]
    func downloadSave(gameId: String, into directory: URL) async throws -> DownloadedFile
    func downloadSaveClock(gameId: String, into directory: URL) async throws -> DownloadedFile
}

extension APIClient: SaveSyncSource {}

/// What the state sync needs from a console (M19). A ``SaveSyncSource``
/// that is not one syncs the saves only.
public protocol StateSyncSource: Sendable {
    func allStates() async throws -> StateList
    func downloadState(gameId: String, slot: Int, into directory: URL) async throws -> DownloadedFile
    func downloadStateThumbnail(gameId: String, slot: Int, into directory: URL) async throws -> DownloadedFile
}

extension APIClient: StateSyncSource {}

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
    /// States listed by the console (M19; 0 for a pak without `GET /states`).
    public var statesTotal: Int
    /// States compared so far.
    public var statesChecked: Int
    /// New state versions stored so far.
    public var statesStored: Int

    public init(
        total: Int = 0, checked: Int = 0, stored: Int = 0, currentGame: String? = nil,
        statesTotal: Int = 0, statesChecked: Int = 0, statesStored: Int = 0
    ) {
        self.total = total
        self.checked = checked
        self.stored = stored
        self.currentGame = currentGame
        self.statesTotal = statesTotal
        self.statesChecked = statesChecked
        self.statesStored = statesStored
    }

    public var fraction: Double {
        let all = total + statesTotal
        return all == 0 ? 0 : Double(checked + statesChecked) / Double(all)
    }
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
    public var id: String { slot.map { "\(gameId)/\($0)" } ?? gameId }
    public let gameId: String
    public let gameName: String
    /// The state slot, for a state (M19); nil for a save.
    public let slot: Int?
    public let error: SyncErrorBox

    public init(gameId: String, gameName: String, slot: Int? = nil, error: any Error) {
        self.gameId = gameId
        self.gameName = gameName
        self.slot = slot
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
    /// M19: the states, nil when the pak has no `GET /states` (older pak).
    public let states: StateSyncReport?
}

/// The outcome of the state part of a sync (M19).
public struct StateSyncReport: Sendable, Equatable {
    /// States listed by the console.
    public let total: Int
    /// States with a new version (`<gameId>/<slot>`).
    public let stored: [String]
    /// States identical to their newest version.
    public let unchanged: Int
    public let failures: [SaveSyncFailure]
    /// The console listed only its first 20000 states.
    public let truncated: Bool
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
        let stateList = try await listStates()
        try Task.checkCancellation()
        // Folder names of the systems: label and tag, as two systems can
        // share a label (GBA and MGBA are both "Game Boy Advance"). Without
        // the list (a busy pak), the tag of the saves folder (`Saves/<TAG>/...`).
        let labels = Dictionary(
            (try? await source.systems())?.map { ($0.id, Self.systemName($0)) } ?? [], uniquingKeysWith: { a, _ in a }
        )

        var state = SaveSyncProgress(total: list.saves.count, statesTotal: stateList?.states.count ?? 0)
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

        // States (M19): the same rules, one series of versions per slot.
        var statesReport: StateSyncReport?
        if let stateList, let stateSource = source as? any StateSyncSource {
            var statesStored: [String] = []
            var statesUnchanged = 0
            var stateFailures: [SaveSyncFailure] = []
            var latestByGame: [String: [Int: StateVersion]] = [:]
            for summary in stateList.states {
                try Task.checkCancellation()
                state.currentGame = summary.gameName
                progress(state)
                do {
                    if latestByGame[summary.gameId] == nil {
                        latestByGame[summary.gameId] = try await vault.latestStateVersions(deviceId: console.deviceId, gameId: summary.gameId)
                    }
                    let latest = latestByGame[summary.gameId]?[summary.slot]
                    if Self.needsNewVersion(summary, latest: latest) {
                        try await sync(summary, from: stateSource, systemName: labels[summary.systemId] ?? Self.systemName(fromStatePath: summary.path))
                        statesStored.append(summary.id)
                        state.statesStored += 1
                    } else {
                        statesUnchanged += 1
                    }
                } catch {
                    if Self.stopsSync(error) { throw error }
                    stateFailures.append(SaveSyncFailure(gameId: summary.gameId, gameName: summary.gameName, slot: summary.slot, error: error))
                }
                state.statesChecked += 1
                state.currentGame = nil
                progress(state)
            }
            statesReport = StateSyncReport(
                total: stateList.states.count, stored: statesStored, unchanged: statesUnchanged,
                failures: stateFailures, truncated: stateList.truncated
            )
        }
        return SaveSyncReport(
            location: vault.location, total: list.saves.count, stored: stored, unchanged: unchanged,
            failures: failures, truncated: list.truncated, finishedAt: Date(), states: statesReport
        )
    }

    /// `GET /states`, nil when the source has no states, the pak no route
    /// (`404`, `405`: a pak older than M19) or the listing failed without
    /// ending the sync (the saves are still synced; the next sync tries
    /// again).
    private func listStates() async throws -> StateList? {
        guard let stateSource = source as? any StateSyncSource else { return nil }
        do {
            return try await stateSource.allStates()
        } catch {
            if Self.stopsSync(error) { throw error }
            return nil
        }
    }

    /// Whether a state needs a new version: its SHA-256 or the SHA-256 of
    /// its thumbnail differ from the newest version of its slot (or there
    /// is none).
    public static func needsNewVersion(_ state: StateSummary, latest: StateVersion?) -> Bool {
        guard let latest else { return true }
        return latest.info.state.sha256 != state.sha256.lowercased()
            || latest.info.thumbnail?.sha256 != state.thumbnail?.sha256.lowercased()
    }

    /// Stores a new version of a state (and its thumbnail), each checked
    /// against the listed SHA-256.
    private func sync(_ summary: StateSummary, from stateSource: any StateSyncSource, systemName: String) async throws {
        let folder = scratchDirectory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        defer { try? FileManager.default.removeItem(at: folder) }

        let stateFile = try await stateSource.downloadState(gameId: summary.gameId, slot: summary.slot, into: folder)
        let stateSHA = try await Self.checked(stateFile.url, expected: summary.sha256)
        var thumbnailFile: NewVersionFile?
        if let thumbnail = summary.thumbnail {
            do {
                let file = try await stateSource.downloadStateThumbnail(gameId: summary.gameId, slot: summary.slot, into: folder)
                // Named after the state (a ".png"), never like the state itself.
                let name = Self.thumbnailName(forState: stateFile.url.lastPathComponent)
                thumbnailFile = NewVersionFile(url: file.url, name: name, sha256: try await Self.checked(file.url, expected: thumbnail.sha256))
            } catch APIError.server(let error) where error.status == 404 {
                // Removed since the listing: the state alone.
            }
        }
        _ = try await vault.addStateVersion(
            console: console,
            game: VaultGameInfo(gameId: summary.gameId, systemId: summary.systemId, systemName: systemName, name: summary.gameName),
            slot: summary.slot,
            state: NewVersionFile(url: stateFile.url, name: stateFile.url.lastPathComponent, sha256: stateSHA),
            thumbnail: thumbnailFile, modifiedAt: summary.modifiedAt
        )
    }

    /// `Game.gba.st0.png` for `Game.gba.st0`.
    static func thumbnailName(forState name: String) -> String { name + ".png" }

    /// The tag of a state folder (`.userdata/shared/GBA-gpsp/Game.gba.st0`
    /// gives `GBA`), else `_`.
    static func systemName(fromStatePath path: String) -> String {
        let parts = path.split(separator: "/")
        guard parts.count >= 2 else { return "_" }
        let folder = parts[parts.count - 2]
        return String(folder.split(separator: "-").first ?? folder)
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
