import Foundation

// Codable models of protocol/openapi.yaml for the routes used by the app.
// Decoding ignores unknown fields (docs/specs/03-protocole.md §9), and the
// enumerations of the contract are open: an unknown value is kept as is
// instead of failing the whole response.

// MARK: - Open enumerations

/// Handheld model (`Model` in the contract).
public struct DeviceModel: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let brick = DeviceModel(rawValue: "brick")
    public static let brickPro = DeviceModel(rawValue: "brickpro")
    public static let smartPro = DeviceModel(rawValue: "smartpro")
    public static let smartProS = DeviceModel(rawValue: "smartpros")
    public static let h700 = DeviceModel(rawValue: "h700")
    public static let my355 = DeviceModel(rawValue: "my355")
    public static let desktop = DeviceModel(rawValue: "desktop")

    /// A name for display; product names are not translated.
    public var displayName: String {
        switch self {
        case .brick: "TrimUI Brick"
        case .brickPro: "TrimUI Brick Pro"
        case .smartPro: "TrimUI Smart Pro"
        case .smartProS: "TrimUI Smart Pro S"
        case .h700: "H700"
        case .my355: "MY355"
        case .desktop: "Desktop"
        default: rawValue
        }
    }
}

/// `file`, `m3u` or `folder-m3u`.
public struct GameKind: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let file = GameKind(rawValue: "file")
    public static let m3u = GameKind(rawValue: "m3u")
    public static let folderM3U = GameKind(rawValue: "folder-m3u")
}

/// Role of a file that belongs to a game.
public struct GameFileRole: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let rom = GameFileRole(rawValue: "rom")
    public static let save = GameFileRole(rawValue: "save")
    public static let state = GameFileRole(rawValue: "state")
    public static let cheats = GameFileRole(rawValue: "cheats")
    public static let art = GameFileRole(rawValue: "art")
}

// MARK: - Device

/// `GET /hello` (unsigned).
public struct Hello: Codable, Sendable, Equatable {
    public let deviceId: String
    public let deviceName: String
    public let model: DeviceModel
    public let platform: String
    public let os: String
    public let osVersion: String?
    public let pakVersion: String
    public let apiVersion: Int
    public let minClientVersion: String
    public let serverTime: Date
    public let pairingOpen: Bool

    public init(
        deviceId: String, deviceName: String, model: DeviceModel, platform: String, os: String,
        osVersion: String?, pakVersion: String, apiVersion: Int, minClientVersion: String,
        serverTime: Date, pairingOpen: Bool
    ) {
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.model = model
        self.platform = platform
        self.os = os
        self.osVersion = osVersion
        self.pakVersion = pakVersion
        self.apiVersion = apiVersion
        self.minClientVersion = minClientVersion
        self.serverTime = serverTime
        self.pairingOpen = pairingOpen
    }
}

/// `GET /device`.
public struct DeviceStatus: Codable, Sendable, Equatable {
    public struct Network: Codable, Sendable, Equatable {
        public let ipv4: String
        public let ssid: String?
        public init(ipv4: String, ssid: String?) {
            self.ipv4 = ipv4
            self.ssid = ssid
        }
    }

    public struct Battery: Codable, Sendable, Equatable {
        public let percent: Int
        public let charging: Bool
        public init(percent: Int, charging: Bool) {
            self.percent = percent
            self.charging = charging
        }
    }

    public struct Storage: Codable, Sendable, Equatable, Identifiable {
        public let id: String
        public let label: String
        public let totalBytes: Int64
        public let freeBytes: Int64
        public init(id: String, label: String, totalBytes: Int64, freeBytes: Int64) {
            self.id = id
            self.label = label
            self.totalBytes = totalBytes
            self.freeBytes = freeBytes
        }

        public var usedBytes: Int64 { max(0, totalBytes - freeBytes) }
    }

    public let deviceId: String
    public let deviceName: String
    public let model: DeviceModel
    public let platform: String
    public let os: String
    public let osVersion: String?
    public let pakVersion: String
    public let network: Network
    public let battery: Battery?
    public let storage: [Storage]
    public let uptimeSeconds: Int64

    public init(
        deviceId: String, deviceName: String, model: DeviceModel, platform: String, os: String,
        osVersion: String?, pakVersion: String, network: Network, battery: Battery?,
        storage: [Storage], uptimeSeconds: Int64
    ) {
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.model = model
        self.platform = platform
        self.os = os
        self.osVersion = osVersion
        self.pakVersion = pakVersion
        self.network = network
        self.battery = battery
        self.storage = storage
        self.uptimeSeconds = uptimeSeconds
    }
}

// MARK: - Library

/// One system of `GET /systems`.
public struct GameSystem: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let id: String
    public let label: String
    public let folder: String
    public let tag: String
    public let gameCount: Int

    public init(id: String, label: String, folder: String, tag: String, gameCount: Int) {
        self.id = id
        self.label = label
        self.folder = folder
        self.tag = tag
        self.gameCount = gameCount
    }
}

public struct SystemList: Codable, Sendable, Equatable {
    public let systems: [GameSystem]
    public init(systems: [GameSystem]) { self.systems = systems }
}

/// A subfolder of a system.
public struct LibraryFolder: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let gameCount: Int

    public init(id: String, name: String, gameCount: Int) {
        self.id = id
        self.name = name
        self.gameCount = gameCount
    }
}

/// A game in a folder listing.
public struct GameSummary: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let id: String
    public let name: String
    public let fileName: String
    public let kind: GameKind
    public let sizeBytes: Int64
    public let modifiedAt: Date
    public let hasSave: Bool
    public let stateCount: Int
    public let lockedStateCount: Int
    public let cheatCount: Int
    public let hasArt: Bool

    public init(
        id: String, name: String, fileName: String, kind: GameKind, sizeBytes: Int64, modifiedAt: Date,
        hasSave: Bool, stateCount: Int, lockedStateCount: Int, cheatCount: Int, hasArt: Bool
    ) {
        self.id = id
        self.name = name
        self.fileName = fileName
        self.kind = kind
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
        self.hasSave = hasSave
        self.stateCount = stateCount
        self.lockedStateCount = lockedStateCount
        self.cheatCount = cheatCount
        self.hasArt = hasArt
    }
}

/// `GET /systems/{systemId}/games`.
public struct GameListing: Codable, Sendable, Equatable {
    public let folders: [LibraryFolder]
    public let games: [GameSummary]

    public init(folders: [LibraryFolder], games: [GameSummary]) {
        self.folders = folders
        self.games = games
    }
}

/// A file of a game. `path` is informative only and never sent back.
public struct GameFile: Codable, Sendable, Equatable, Hashable {
    public let role: GameFileRole
    public let slot: Int?
    public let path: String
    public let sizeBytes: Int64
    public let modifiedAt: Date

    public init(role: GameFileRole, slot: Int? = nil, path: String, sizeBytes: Int64, modifiedAt: Date) {
        self.role = role
        self.slot = slot
        self.path = path
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
    }
}

/// `GET /games/{gameId}`.
public struct GameDetail: Codable, Sendable, Equatable {
    public let id: String
    public let systemId: String
    public let name: String
    public let fileName: String
    public let kind: GameKind
    public let files: [GameFile]
    public let totalBytes: Int64

    public init(
        id: String, systemId: String, name: String, fileName: String, kind: GameKind,
        files: [GameFile], totalBytes: Int64
    ) {
        self.id = id
        self.systemId = systemId
        self.name = name
        self.fileName = fileName
        self.kind = kind
        self.files = files
        self.totalBytes = totalBytes
    }

    public var hasSave: Bool { files.contains { $0.role == .save } }
    public var stateCount: Int { files.filter { $0.role == .state }.count }
    public var hasCheats: Bool { files.contains { $0.role == .cheats } }
}

// MARK: - Trash

public struct TrashEntry: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let id: String
    public let trashedAt: Date
    public let reason: String
    public let label: String
    public let files: [String]
    public let sizeBytes: Int64
    public let expiresAt: Date

    public init(
        id: String, trashedAt: Date, reason: String, label: String, files: [String],
        sizeBytes: Int64, expiresAt: Date
    ) {
        self.id = id
        self.trashedAt = trashedAt
        self.reason = reason
        self.label = label
        self.files = files
        self.sizeBytes = sizeBytes
        self.expiresAt = expiresAt
    }
}

/// `GET /trash`.
public struct TrashList: Codable, Sendable, Equatable {
    public let entries: [TrashEntry]
    public let totalBytes: Int64

    public init(entries: [TrashEntry], totalBytes: Int64) {
        self.entries = entries
        self.totalBytes = totalBytes
    }
}

/// `DELETE /trash` and `DELETE /trash/{entryId}`.
public struct FreedBytes: Codable, Sendable, Equatable {
    public let freedBytes: Int64
    public init(freedBytes: Int64) { self.freedBytes = freedBytes }
}

// MARK: - Errors

/// `{ "error": { "code", "message", "details" } }`.
public struct ErrorEnvelope: Codable, Sendable, Equatable {
    public struct Body: Codable, Sendable, Equatable {
        public let code: String
        public let message: String
        public let details: [String: JSONValue]?

        public init(code: String, message: String, details: [String: JSONValue]? = [:]) {
            self.code = code
            self.message = message
            self.details = details
        }
    }

    public let error: Body
    public init(error: Body) { self.error = error }
}

// MARK: - JSON coding

public enum ProtocolJSON {
    /// Decoder for responses: RFC 3339 dates, with or without fractional seconds.
    public static func decoder() -> JSONDecoder {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let c = try decoder.singleValueContainer()
            let s = try c.decode(String.self)
            if let date = parseDate(s) { return date }
            throw DecodingError.dataCorruptedError(in: c, debugDescription: "Not an RFC 3339 date: \(s)")
        }
        return decoder
    }

    /// Encoder for bodies: RFC 3339 UTC dates without fractional seconds.
    public static func encoder() -> JSONEncoder {
        let encoder = JSONEncoder()
        encoder.dateEncodingStrategy = .custom { date, encoder in
            var c = encoder.singleValueContainer()
            try c.encode(formatDate(date))
        }
        encoder.outputFormatting = [.sortedKeys, .withoutEscapingSlashes]
        return encoder
    }

    public static func parseDate(_ s: String) -> Date? {
        if let d = try? Date(s, strategy: Date.ISO8601FormatStyle()) { return d }
        return try? Date(s, strategy: Date.ISO8601FormatStyle(includingFractionalSeconds: true))
    }

    public static func formatDate(_ date: Date) -> String {
        date.formatted(Date.ISO8601FormatStyle())
    }
}
