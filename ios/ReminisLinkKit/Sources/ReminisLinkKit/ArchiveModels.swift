import Foundation

// Codable models of the game archives of milestone M19 (protocol/openapi.yaml,
// tag Archives): `GET /games/{gameId}/archive`, `GET /archives/{stagingId}`
// and `POST /archives/{stagingId}/import`.

/// What a file of a game archive is (`ArchiveRole`; open: an unknown role
/// is kept). An importer maps a file by its role, never by its path.
public struct ArchiveRole: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let save = ArchiveRole(rawValue: "save")
    public static let rtc = ArchiveRole(rawValue: "rtc")
    public static let state = ArchiveRole(rawValue: "state")
    public static let stateThumbnail = ArchiveRole(rawValue: "state-thumbnail")
    public static let cheats = ArchiveRole(rawValue: "cheats")
    public static let art = ArchiveRole(rawValue: "art")
    public static let rom = ArchiveRole(rawValue: "rom")
}

/// `GameArchiveSource`: where an archive comes from (informative).
public struct GameArchiveSource: Codable, Sendable, Equatable, Hashable {
    public let deviceName: String?
    public let os: String
    public let platform: String?
    public let pakVersion: String?
    public let systemTag: String
    public let systemLabel: String
    public let gameName: String
    public let fileName: String
    public let romName: String
    public let kind: String

    public init(
        deviceName: String?, os: String, platform: String?, pakVersion: String?, systemTag: String,
        systemLabel: String, gameName: String, fileName: String, romName: String, kind: String
    ) {
        self.deviceName = deviceName
        self.os = os
        self.platform = platform
        self.pakVersion = pakVersion
        self.systemTag = systemTag
        self.systemLabel = systemLabel
        self.gameName = gameName
        self.fileName = fileName
        self.romName = romName
        self.kind = kind
    }
}

/// `GameArchiveFile`: a file of the manifest.
public struct GameArchiveFile: Codable, Sendable, Equatable, Hashable {
    public let path: String
    public let role: ArchiveRole
    public let slot: Int?
    public let sizeBytes: Int64
    public let sha256: String

    public init(path: String, role: ArchiveRole, slot: Int?, sizeBytes: Int64, sha256: String) {
        self.path = path
        self.role = role
        self.slot = slot
        self.sizeBytes = sizeBytes
        self.sha256 = sha256
    }
}

/// `GameArchiveManifest` (`manifest.json`, format version 1).
public struct GameArchiveManifest: Codable, Sendable, Equatable, Hashable {
    public static let formatName = "reminislink-game-archive"
    public static let version = 1
    /// The end of the name of an exported archive.
    public static let fileSuffix = ".reminislink.zip"

    public let format: String
    public let version: Int
    public let createdAt: Date
    public let source: GameArchiveSource
    public let files: [GameArchiveFile]

    public init(format: String = Self.formatName, version: Int = Self.version, createdAt: Date, source: GameArchiveSource, files: [GameArchiveFile]) {
        self.format = format
        self.version = version
        self.createdAt = createdAt
        self.source = source
        self.files = files
    }

    /// Whether the archive holds a ROM (it can then make a new game).
    public var hasROM: Bool { files.contains { $0.role == .rom } }

    /// Whether a file name looks like an exported game archive.
    public static func isArchiveName(_ name: String) -> Bool {
        name.lowercased().hasSuffix(fileSuffix)
    }
}

/// `ArchiveTarget`: an existing game, or a folder of a system for a new
/// game made from the archive's ROM.
public enum ArchiveTarget: Sendable, Hashable, Codable {
    case game(gameId: String)
    case newGame(systemId: String, folderId: String?)

    private enum CodingKeys: String, CodingKey { case gameId, systemId, folderId }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        if let gameId = try c.decodeIfPresent(String.self, forKey: .gameId) {
            self = .game(gameId: gameId)
        } else {
            self = .newGame(
                systemId: try c.decode(String.self, forKey: .systemId),
                folderId: try c.decodeIfPresent(String.self, forKey: .folderId)
            )
        }
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .game(let gameId):
            try c.encode(gameId, forKey: .gameId)
        case .newGame(let systemId, let folderId):
            try c.encode(systemId, forKey: .systemId)
            try c.encodeIfPresent(folderId, forKey: .folderId)
        }
    }

    /// The query of `GET /archives/{stagingId}`.
    var query: [(name: String, value: String)] {
        switch self {
        case .game(let gameId): [("gameId", gameId)]
        case .newGame(let systemId, let folderId): [("systemId", systemId)] + (folderId.map { [("folderId", $0)] } ?? [])
        }
    }

    public var gameId: String? {
        if case .game(let id) = self { return id }
        return nil
    }
}

/// What the import does with one file (`ArchivePlanEntry.action`; open).
public struct ArchiveAction: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let create = ArchiveAction(rawValue: "create")
    public static let replace = ArchiveAction(rawValue: "replace")
    public static let skip = ArchiveAction(rawValue: "skip")
}

/// Why a file is skipped (`ArchivePlanEntry.reason`; open: an unknown
/// reason is shown as skipped).
public struct ArchiveSkipReason: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let gameExists = ArchiveSkipReason(rawValue: "game-exists")
    public static let locked = ArchiveSkipReason(rawValue: "locked")
    public static let unsupportedFormat = ArchiveSkipReason(rawValue: "unsupported-format")
    public static let noStateFolder = ArchiveSkipReason(rawValue: "no-state-folder")
    public static let noState = ArchiveSkipReason(rawValue: "no-state")
    public static let noTarget = ArchiveSkipReason(rawValue: "no-target")
}

/// `ArchivePlanEntry`.
public struct ArchivePlanEntry: Codable, Sendable, Equatable, Hashable {
    public let role: ArchiveRole
    public let slot: Int?
    public let sizeBytes: Int64
    public let action: ArchiveAction
    public let reason: ArchiveSkipReason?

    public init(role: ArchiveRole, slot: Int?, sizeBytes: Int64, action: ArchiveAction, reason: ArchiveSkipReason?) {
        self.role = role
        self.slot = slot
        self.sizeBytes = sizeBytes
        self.action = action
        self.reason = reason
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(role, forKey: .role)
        try c.encode(slot, forKey: .slot)
        try c.encode(sizeBytes, forKey: .sizeBytes)
        try c.encode(action, forKey: .action)
        try c.encode(reason, forKey: .reason)
    }
}

/// `ArchivePlan`: the answer of `GET /archives/{stagingId}`.
public struct ArchivePlan: Codable, Sendable, Equatable {
    public let manifest: GameArchiveManifest
    /// The target used (given or suggested), nil when none fits.
    public let target: ArchiveTarget?
    /// The target game's name, or the system label for a new game.
    public let targetName: String?
    /// The pak chose the target.
    public let suggested: Bool
    public let entries: [ArchivePlanEntry]
    public let importBytes: Int64

    public init(
        manifest: GameArchiveManifest, target: ArchiveTarget?, targetName: String?, suggested: Bool,
        entries: [ArchivePlanEntry], importBytes: Int64
    ) {
        self.manifest = manifest
        self.target = target
        self.targetName = targetName
        self.suggested = suggested
        self.entries = entries
        self.importBytes = importBytes
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(manifest, forKey: .manifest)
        try c.encode(target, forKey: .target)
        try c.encode(targetName, forKey: .targetName)
        try c.encode(suggested, forKey: .suggested)
        try c.encode(entries, forKey: .entries)
        try c.encode(importBytes, forKey: .importBytes)
    }

    /// Files the import replaces (their current version goes to the trash).
    public var replaced: [ArchivePlanEntry] { entries.filter { $0.action == .replace } }
    /// Files the import adds.
    public var created: [ArchivePlanEntry] { entries.filter { $0.action == .create } }
    /// Files left out.
    public var skipped: [ArchivePlanEntry] { entries.filter { $0.action != .create && $0.action != .replace } }
    /// Whether the import would write anything.
    public var importsAnything: Bool { target != nil && entries.contains { $0.action == .create || $0.action == .replace } }
}

/// `ArchiveImportRequest`.
public struct ArchiveImportRequest: Codable, Sendable, Equatable {
    public let target: ArchiveTarget
    public init(target: ArchiveTarget) { self.target = target }
}

/// The result of a succeeded `archive-import` job.
public struct ArchiveImportResult: Sendable, Equatable {
    public struct Skipped: Sendable, Equatable {
        public let role: ArchiveRole
        public let slot: Int?
        public let reason: ArchiveSkipReason
    }

    public let gameId: String
    public let imported: Int
    public let skipped: [Skipped]
    public let trashEntryId: String?
    public let saveBackupId: String?

    /// Reads the `result` of a job; nil when it is not an import result.
    public init?(_ result: [String: JSONValue]?) {
        guard let result, case .string(let gameId) = result["gameId"] else { return nil }
        self.gameId = gameId
        imported = Int(result["imported"]?.int64Value ?? 0)
        var skipped: [Skipped] = []
        if case .array(let items) = result["skipped"] {
            for item in items {
                guard case .object(let o) = item, case .string(let role) = o["role"], case .string(let reason) = o["reason"] else { continue }
                skipped.append(Skipped(role: ArchiveRole(rawValue: role), slot: o["slot"]?.int64Value.map(Int.init), reason: ArchiveSkipReason(rawValue: reason)))
            }
        }
        self.skipped = skipped
        if case .string(let id) = result["trashEntryId"] { trashEntryId = id } else { trashEntryId = nil }
        if case .string(let id) = result["saveBackupId"] { saveBackupId = id } else { saveBackupId = nil }
    }
}

extension JobKind {
    /// M19: `POST /archives/{stagingId}/import`.
    public static let archiveImport = JobKind(rawValue: "archive-import")
}

extension BackupReason {
    /// M19: the save before the import of a game archive.
    public static let beforeImport = BackupReason(rawValue: "before-import")
}

// MARK: - GET /states (M19)

/// `StateThumbnailFile`.
public struct StateThumbnailFile: Codable, Sendable, Equatable, Hashable {
    public let sizeBytes: Int64
    public let sha256: String

    public init(sizeBytes: Int64, sha256: String) {
        self.sizeBytes = sizeBytes
        self.sha256 = sha256
    }
}

/// `StateSummary`: a state of one game, with its SHA-256.
public struct StateSummary: Codable, Sendable, Equatable, Hashable, Identifiable {
    public var id: String { "\(gameId)/\(slot)" }
    public let gameId: String
    public let systemId: String
    public let gameName: String
    public let fileName: String
    public let slot: Int
    /// Informative, relative to the SD card root. Never sent back.
    public let path: String
    public let sizeBytes: Int64
    /// From the console clock (informative): compare `sha256`.
    public let modifiedAt: Date
    public let sha256: String
    public let thumbnail: StateThumbnailFile?

    public init(
        gameId: String, systemId: String, gameName: String, fileName: String, slot: Int, path: String,
        sizeBytes: Int64, modifiedAt: Date, sha256: String, thumbnail: StateThumbnailFile?
    ) {
        self.gameId = gameId
        self.systemId = systemId
        self.gameName = gameName
        self.fileName = fileName
        self.slot = slot
        self.path = path
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
        self.sha256 = sha256
        self.thumbnail = thumbnail
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(gameId, forKey: .gameId)
        try c.encode(systemId, forKey: .systemId)
        try c.encode(gameName, forKey: .gameName)
        try c.encode(fileName, forKey: .fileName)
        try c.encode(slot, forKey: .slot)
        try c.encode(path, forKey: .path)
        try c.encode(sizeBytes, forKey: .sizeBytes)
        try c.encode(modifiedAt, forKey: .modifiedAt)
        try c.encode(sha256, forKey: .sha256)
        // Required by the contract, null when there is no thumbnail.
        try c.encode(thumbnail, forKey: .thumbnail)
    }
}

/// `GET /states`.
public struct StateList: Codable, Sendable, Equatable {
    public let states: [StateSummary]
    /// More than 20000 states: the list is incomplete.
    public let truncated: Bool
    public let generatedAt: Date

    public init(states: [StateSummary], truncated: Bool, generatedAt: Date) {
        self.states = states
        self.truncated = truncated
        self.generatedAt = generatedAt
    }
}
