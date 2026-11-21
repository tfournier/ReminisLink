import Foundation

// Saves copied from the consoles to the iPhone (milestone M9,
// docs/specs/09-feuille-de-route.md): one folder per console, system and
// game, holding up to ``SaveVaultLimits/maxVersions`` versions of the save
// (with its `.rtc`), each in a folder named by the date of the sync:
//
//     <root>/<console>/<system>/<game>/2026-11-19 15.42.10/Game.gba.sav
//                                                         /Game.gba.rtc
//
// The root is the "ReminisLink" folder of the user's iCloud Drive, or the
// app's own Documents folder when iCloud is unavailable.
//
// M19: the states are versioned the same way, in the same game folder, one
// series of versions per slot, each version in a folder named by the date
// of the sync and the slot, with its own manifest (`.state-version.json`,
// never read as a save version):
//
//     <root>/<console>/<system>/<game>/2026-11-19 15.42.10 state 3/Game.gba.st3
//                                                                  /Game.gba.3.bmp

/// Where the saves are kept.
public enum SaveVaultLocation: String, Sendable, Codable {
    /// The app's folder in the user's iCloud Drive.
    case iCloud
    /// The app's Documents folder on this iPhone (iCloud unavailable or off).
    case local
    /// A temporary folder of the demo mode (milestone M16), erased when
    /// the demo ends: never iCloud Drive nor the user's Documents.
    case demo
}

public enum SaveVaultLimits {
    /// Versions kept per save (and per state slot, M19); older ones are
    /// deleted when a new one is added.
    public static let maxVersions = 10
}

/// Who a console folder belongs to (`.console.json`).
public struct VaultConsoleInfo: Codable, Sendable, Hashable {
    public let deviceId: String
    /// The console name when its folder was made (the folder keeps it).
    public let name: String

    public init(deviceId: String, name: String) {
        self.deviceId = deviceId
        self.name = name
    }
}

/// Which game a game folder belongs to (`.game.json`).
public struct VaultGameInfo: Codable, Sendable, Hashable {
    public let gameId: String
    public let systemId: String
    /// The name of the system folder.
    public let systemName: String
    /// The game name (the name of its folder, made safe).
    public let name: String

    public init(gameId: String, systemId: String, systemName: String, name: String) {
        self.gameId = gameId
        self.systemId = systemId
        self.systemName = systemName
        self.name = name
    }
}

/// One file of a version.
public struct SaveVersionFile: Codable, Sendable, Hashable {
    public let name: String
    /// Lowercase hex SHA-256.
    public let sha256: String
    public let sizeBytes: Int64

    public init(name: String, sha256: String, sizeBytes: Int64) {
        self.name = name
        self.sha256 = sha256
        self.sizeBytes = sizeBytes
    }
}

/// What a version holds (`.version.json`, written with its files).
public struct SaveVersionInfo: Codable, Sendable, Hashable {
    public let gameId: String
    /// Publication order in its game folder (1 for the first version):
    /// versions are ordered and kept by it, never by a date, as the clock
    /// of the iPhone can go back. 0 for a version written before it existed
    /// (first M9 builds): those rank before every numbered version, by
    /// `syncedAt` then folder name.
    public let sequence: Int
    /// When the iPhone copied it.
    public let syncedAt: Date
    /// When the console last wrote the save (its clock may be wrong).
    public let modifiedAt: Date?
    public let save: SaveVersionFile
    /// The clock file of the game, when the console has one.
    public let rtc: SaveVersionFile?

    public init(gameId: String, sequence: Int, syncedAt: Date, modifiedAt: Date?, save: SaveVersionFile, rtc: SaveVersionFile?) {
        self.gameId = gameId
        self.sequence = sequence
        self.syncedAt = syncedAt
        self.modifiedAt = modifiedAt
        self.save = save
        self.rtc = rtc
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        gameId = try c.decode(String.self, forKey: .gameId)
        sequence = try c.decodeIfPresent(Int.self, forKey: .sequence) ?? 0
        syncedAt = try c.decode(Date.self, forKey: .syncedAt)
        modifiedAt = try c.decodeIfPresent(Date.self, forKey: .modifiedAt)
        save = try c.decode(SaveVersionFile.self, forKey: .save)
        rtc = try c.decodeIfPresent(SaveVersionFile.self, forKey: .rtc)
    }
}

/// The manifest of a version in the vault: `.version.json` (a save) or
/// `.state-version.json` (a state, M19). Versions of one game are numbered,
/// shown, limited and found per series (the save, or one state slot), with
/// the same rules.
public protocol VaultVersionRecord: Codable, Sendable, Hashable {
    /// The name of the manifest file in the version folder.
    static var manifestName: String { get }
    var gameId: String { get }
    var sequence: Int { get }
    var syncedAt: Date { get }
    var modifiedAt: Date? { get }
    /// The files of the version, the main one first.
    var files: [SaveVersionFile] { get }
    /// `save`, or `state/<slot>`.
    var series: String { get }
    /// What tells two versions apart when everything else is equal (the
    /// same on every device).
    var manifestKey: String { get }
}

extension SaveVersionInfo: VaultVersionRecord {
    public static let manifestName = ".version.json"
    public var files: [SaveVersionFile] { [save] + (rtc.map { [$0] } ?? []) }
    public var series: String { "save" }
    public var manifestKey: String {
        [save, rtc].map { file in file.map { "\($0.sha256)/\($0.sizeBytes)/\($0.name)" } ?? "-" }.joined(separator: "|")
            + "|\(modifiedAt?.timeIntervalSince1970 ?? 0)"
    }
}

/// What a state version holds (`.state-version.json`, M19).
public struct StateVersionInfo: VaultVersionRecord {
    public static let manifestName = ".state-version.json"
    public let gameId: String
    public let slot: Int
    /// Publication order among the versions of this slot (1 for the first).
    public let sequence: Int
    public let syncedAt: Date
    /// When the console last wrote the state (its clock may be wrong).
    public let modifiedAt: Date?
    public let state: SaveVersionFile
    /// The thumbnail of the state (a PNG), when the console has one.
    public let thumbnail: SaveVersionFile?

    public init(gameId: String, slot: Int, sequence: Int, syncedAt: Date, modifiedAt: Date?, state: SaveVersionFile, thumbnail: SaveVersionFile?) {
        self.gameId = gameId
        self.slot = slot
        self.sequence = sequence
        self.syncedAt = syncedAt
        self.modifiedAt = modifiedAt
        self.state = state
        self.thumbnail = thumbnail
    }

    public var files: [SaveVersionFile] { [state] + (thumbnail.map { [$0] } ?? []) }
    public var series: String { "state/\(slot)" }
    public var manifestKey: String {
        [state, thumbnail].map { file in file.map { "\($0.sha256)/\($0.sizeBytes)/\($0.name)" } ?? "-" }.joined(separator: "|")
            + "|\(modifiedAt?.timeIntervalSince1970 ?? 0)"
    }
}

/// A stored version of a state (M19).
public struct StateVersion: Sendable, Hashable, Identifiable {
    /// The name of its folder (or its path in the vault, when it is in
    /// another folder of the game).
    public let id: String
    public let info: StateVersionInfo

    public init(id: String, info: StateVersionInfo) {
        self.id = id
        self.info = info
    }
}

/// Copies of a state version's files, outside the vault.
public struct ExportedStateVersion: Sendable, Equatable {
    public let state: URL
    public let thumbnail: URL?
    /// The folder holding them: delete it once used.
    public var directory: URL { state.deletingLastPathComponent() }
}

/// A stored version of a save.
public struct SaveVersion: Sendable, Hashable, Identifiable {
    /// The name of its folder (unique in its game folder).
    public let id: String
    public let info: SaveVersionInfo

    public init(id: String, info: SaveVersionInfo) {
        self.id = id
        self.info = info
    }
}

public struct VaultConsole: Sendable, Hashable, Identifiable {
    public var id: String { info.deviceId }
    public let info: VaultConsoleInfo
    /// The folder name in the vault.
    public let folderName: String
}

public struct VaultGame: Sendable, Hashable, Identifiable {
    public var id: String { info.gameId }
    public let info: VaultGameInfo
    /// The folder path in the console folder: `<system>/<game>`.
    public let folderPath: String
    /// The newest version, if any.
    public let latest: SaveVersion?
    public let versionCount: Int
    /// The slots with at least one state version (M19), in order.
    public let stateSlots: [Int]
    /// The newest state version of every slot (M19), by publication date.
    public let latestState: StateVersion?

    public init(info: VaultGameInfo, folderPath: String, latest: SaveVersion?, versionCount: Int, stateSlots: [Int] = [], latestState: StateVersion? = nil) {
        self.info = info
        self.folderPath = folderPath
        self.latest = latest
        self.versionCount = versionCount
        self.stateSlots = stateSlots
        self.latestState = latestState
    }
}

/// A file to store in a new version. The vault takes it (moves it).
public struct NewVersionFile: Sendable {
    public let url: URL
    public let name: String
    public let sha256: String

    public init(url: URL, name: String, sha256: String) {
        self.url = url
        self.name = name
        self.sha256 = sha256
    }
}

/// Copies of a version's files, outside the vault (to restore or share).
public struct ExportedVersion: Sendable, Equatable {
    public let save: URL
    public let rtc: URL?
    /// The folder holding them: delete it once used.
    public var directory: URL { save.deletingLastPathComponent() }
}

public enum SaveVaultError: Error, Equatable, Sendable {
    case versionNotFound
    /// The copy read back from the vault is not the version chosen.
    case versionMismatch
    /// A file name the vault cannot use (empty, a path...).
    case invalidName(String)
}

/// The store of synced saves. ``FileSaveVault`` keeps them in a folder
/// (iCloud Drive, the app's Documents, or a temporary folder in tests).
public protocol SaveVault: Sendable {
    var location: SaveVaultLocation { get }
    /// The consoles with a folder, by name.
    func consoles() async throws -> [VaultConsole]
    /// The games of a console that have at least one version, by system then name.
    func games(deviceId: String) async throws -> [VaultGame]
    /// The versions of a game, last published first.
    func versions(deviceId: String, gameId: String) async throws -> [SaveVersion]
    /// Stores a new version (the files are moved in) with the next
    /// sequence number, then deletes the oldest versions beyond
    /// ``SaveVaultLimits/maxVersions`` (never the new one). This is the
    /// only time versions are deleted: a rotation left unfinished (the app
    /// stopped between the move and the deletion) is finished at the next
    /// version of that game.
    func addVersion(
        console: VaultConsoleInfo, game: VaultGameInfo,
        save: NewVersionFile, rtc: NewVersionFile?, modifiedAt: Date?
    ) async throws -> SaveVersion
    /// Copies the files of a version into a new folder under `directory`.
    /// The version is found by its content and metadata, not by its
    /// folder: it may have moved since it was listed.
    func export(deviceId: String, gameId: String, version: SaveVersion, to directory: URL) async throws -> ExportedVersion

    // States (M19): the same rules, one series of versions per slot.

    /// The state versions of a game, by slot, then last published first.
    func stateVersions(deviceId: String, gameId: String) async throws -> [StateVersion]
    /// Stores a new version of a state slot, then deletes the oldest
    /// versions of that slot beyond ``SaveVaultLimits/maxVersions``.
    func addStateVersion(
        console: VaultConsoleInfo, game: VaultGameInfo, slot: Int,
        state: NewVersionFile, thumbnail: NewVersionFile?, modifiedAt: Date?
    ) async throws -> StateVersion
    /// Copies the files of a state version into a new folder under `directory`.
    func exportState(deviceId: String, gameId: String, version: StateVersion, to directory: URL) async throws -> ExportedStateVersion
}

extension SaveVault {
    public func latestVersion(deviceId: String, gameId: String) async throws -> SaveVersion? {
        try await versions(deviceId: deviceId, gameId: gameId).first
    }

    /// Copies a version out of the vault and checks that the save (and its
    /// `.rtc`) read back are the ones of `version` (SHA-256): what is sent
    /// to a console is always the version the user confirmed.
    public func exportVerified(
        deviceId: String, gameId: String, version: SaveVersion, to directory: URL
    ) async throws -> ExportedVersion {
        let exported = try await export(deviceId: deviceId, gameId: gameId, version: version, to: directory)
        do {
            let save = try await FileDigest.sha256(of: exported.save)
            let rtc: String? = if let url = exported.rtc { try await FileDigest.sha256(of: url) } else { nil }
            guard save == version.info.save.sha256, rtc == version.info.rtc?.sha256 else {
                throw SaveVaultError.versionMismatch
            }
            return exported
        } catch {
            try? FileManager.default.removeItem(at: exported.directory)
            throw error
        }
    }

    /// The newest version of every state slot of a game.
    public func latestStateVersions(deviceId: String, gameId: String) async throws -> [Int: StateVersion] {
        var latest: [Int: StateVersion] = [:]
        for version in try await stateVersions(deviceId: deviceId, gameId: gameId) where latest[version.info.slot] == nil {
            latest[version.info.slot] = version
        }
        return latest
    }

    /// ``exportState(deviceId:gameId:version:to:)``, checked against the
    /// SHA-256 of `version`, as ``exportVerified(deviceId:gameId:version:to:)``.
    public func exportStateVerified(
        deviceId: String, gameId: String, version: StateVersion, to directory: URL
    ) async throws -> ExportedStateVersion {
        let exported = try await exportState(deviceId: deviceId, gameId: gameId, version: version, to: directory)
        do {
            let state = try await FileDigest.sha256(of: exported.state)
            let thumbnail: String? = if let url = exported.thumbnail { try await FileDigest.sha256(of: url) } else { nil }
            guard state == version.info.state.sha256, thumbnail == version.info.thumbnail?.sha256 else {
                throw SaveVaultError.versionMismatch
            }
            return exported
        } catch {
            try? FileManager.default.removeItem(at: exported.directory)
            throw error
        }
    }
}
