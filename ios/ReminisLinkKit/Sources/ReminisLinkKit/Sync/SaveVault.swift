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

/// Where the saves are kept.
public enum SaveVaultLocation: String, Sendable, Codable {
    /// The app's folder in the user's iCloud Drive.
    case iCloud
    /// The app's Documents folder on this iPhone (iCloud unavailable or off).
    case local
}

public enum SaveVaultLimits {
    /// Versions kept per save; older ones are deleted when a new one is added.
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
    /// The versions of a game, last published first, after deleting the
    /// oldest beyond ``SaveVaultLimits/maxVersions`` (a rotation left
    /// unfinished, e.g. the app was stopped between the move and the
    /// deletion). The sync calls it before comparing.
    func retainedVersions(deviceId: String, gameId: String) async throws -> [SaveVersion]
    /// Finishes the rotations left unfinished in every game of a console,
    /// also the games the console no longer lists.
    func finishRotations(deviceId: String) async throws
    /// Stores a new version (the files are moved in) with the next
    /// sequence number, then deletes the oldest versions beyond
    /// ``SaveVaultLimits/maxVersions`` (never the new one).
    func addVersion(
        console: VaultConsoleInfo, game: VaultGameInfo,
        save: NewVersionFile, rtc: NewVersionFile?, modifiedAt: Date?
    ) async throws -> SaveVersion
    /// Copies the files of a version into a new folder under `directory`.
    func export(deviceId: String, gameId: String, versionId: String, to directory: URL) async throws -> ExportedVersion
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
        let exported = try await export(deviceId: deviceId, gameId: gameId, versionId: version.id, to: directory)
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
}
