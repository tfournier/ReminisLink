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

/// `file`, `m3u`, `folder-m3u` or `folder-cue`. An unknown kind is treated
/// like `folder-m3u` (the contract).
public struct GameKind: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let file = GameKind(rawValue: "file")
    public static let m3u = GameKind(rawValue: "m3u")
    public static let folderM3U = GameKind(rawValue: "folder-m3u")
    /// M8: a folder holding a `.cue` of the same name and no such `.m3u`.
    public static let folderCUE = GameKind(rawValue: "folder-cue")

    /// The whole folder is the game: `folder-m3u`, `folder-cue` and any
    /// unknown kind.
    public var isFolder: Bool { self != .file && self != .m3u }

    /// Several files may make the game (every kind but `file`).
    public var isMultiDisc: Bool { self != .file }
}

/// Role of a file that belongs to a game.
public struct GameFileRole: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let rom = GameFileRole(rawValue: "rom")
    public static let save = GameFileRole(rawValue: "save")
    /// The clock file NextUI writes next to the save (`.rtc`).
    public static let rtc = GameFileRole(rawValue: "rtc")
    public static let state = GameFileRole(rawValue: "state")
    public static let cheats = GameFileRole(rawValue: "cheats")
    public static let art = GameFileRole(rawValue: "art")
    /// Only in ``DeletionPreview``: any other file (side files of the
    /// states, `.cht.bak`, ReminisLink copies...).
    public static let other = GameFileRole(rawValue: "other")
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
    /// M10: a client holds the write lease (its name is only given to
    /// paired clients). Read leniently: a pak without M10 does not send it.
    public let writerHeld: Bool

    public init(
        deviceId: String, deviceName: String, model: DeviceModel, platform: String, os: String,
        osVersion: String?, pakVersion: String, apiVersion: Int, minClientVersion: String,
        serverTime: Date, pairingOpen: Bool, writerHeld: Bool = false
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
        self.writerHeld = writerHeld
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        deviceId = try c.decode(String.self, forKey: .deviceId)
        deviceName = try c.decode(String.self, forKey: .deviceName)
        model = try c.decode(DeviceModel.self, forKey: .model)
        platform = try c.decode(String.self, forKey: .platform)
        os = try c.decode(String.self, forKey: .os)
        osVersion = try c.decodeIfPresent(String.self, forKey: .osVersion)
        pakVersion = try c.decode(String.self, forKey: .pakVersion)
        apiVersion = try c.decode(Int.self, forKey: .apiVersion)
        minClientVersion = try c.decode(String.self, forKey: .minClientVersion)
        serverTime = try c.decode(Date.self, forKey: .serverTime)
        pairingOpen = try c.decode(Bool.self, forKey: .pairingOpen)
        writerHeld = try c.decodeIfPresent(Bool.self, forKey: .writerHeld) ?? false
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
    /// Paired clients with a signed request accepted in the last 30 s, the
    /// caller included (kept in memory by the pak). Required by the
    /// contract, but read leniently: missing means none.
    public let connectedClients: [ConnectedClient]
    /// M10: the client holding the write lease, or nil when it is free (or
    /// when the pak does not send the field, see ``hasWriterField``).
    public let writer: Writer?
    /// Whether the pak sent `writer` (a pak without M10 does not).
    public let hasWriterField: Bool

    public init(
        deviceId: String, deviceName: String, model: DeviceModel, platform: String, os: String,
        osVersion: String?, pakVersion: String, network: Network, battery: Battery?,
        storage: [Storage], uptimeSeconds: Int64, connectedClients: [ConnectedClient] = [],
        writer: Writer? = nil, hasWriterField: Bool = true
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
        self.connectedClients = connectedClients
        self.writer = writer
        self.hasWriterField = hasWriterField
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        deviceId = try c.decode(String.self, forKey: .deviceId)
        deviceName = try c.decode(String.self, forKey: .deviceName)
        model = try c.decode(DeviceModel.self, forKey: .model)
        platform = try c.decode(String.self, forKey: .platform)
        os = try c.decode(String.self, forKey: .os)
        osVersion = try c.decodeIfPresent(String.self, forKey: .osVersion)
        pakVersion = try c.decode(String.self, forKey: .pakVersion)
        network = try c.decode(Network.self, forKey: .network)
        battery = try c.decodeIfPresent(Battery.self, forKey: .battery)
        storage = try c.decode([Storage].self, forKey: .storage)
        uptimeSeconds = try c.decode(Int64.self, forKey: .uptimeSeconds)
        connectedClients = try c.decodeIfPresent([ConnectedClient].self, forKey: .connectedClients) ?? []
        hasWriterField = c.contains(.writer)
        writer = try c.decodeIfPresent(Writer.self, forKey: .writer)
    }

    private enum CodingKeys: String, CodingKey {
        case deviceId, deviceName, model, platform, os, osVersion, pakVersion, network, battery, storage
        case uptimeSeconds, connectedClients, writer
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(deviceId, forKey: .deviceId)
        try c.encode(deviceName, forKey: .deviceName)
        try c.encode(model, forKey: .model)
        try c.encode(platform, forKey: .platform)
        try c.encode(os, forKey: .os)
        try c.encode(osVersion, forKey: .osVersion)
        try c.encode(pakVersion, forKey: .pakVersion)
        try c.encode(network, forKey: .network)
        try c.encode(battery, forKey: .battery)
        try c.encode(storage, forKey: .storage)
        try c.encode(uptimeSeconds, forKey: .uptimeSeconds)
        try c.encode(connectedClients, forKey: .connectedClients)
        if hasWriterField { try c.encode(writer, forKey: .writer) }
    }
}

// MARK: - Write lease (M10)

/// The client holding the write lease (`Writer` in the contract).
public struct Writer: Codable, Sendable, Equatable, Hashable {
    public let clientName: String
    /// The caller holds the lease.
    public let you: Bool
    public let expiresAt: Date

    public init(clientName: String, you: Bool, expiresAt: Date) {
        self.clientName = clientName
        self.you = you
        self.expiresAt = expiresAt
    }
}

/// `GET /writer` and `PUT /writer`: the holder, or nil when free.
public struct WriterState: Codable, Sendable, Equatable {
    public let writer: Writer?

    public init(writer: Writer?) { self.writer = writer }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(writer, forKey: .writer)
    }
}

/// Body of `PUT /writer`.
public struct TakeWriterRequest: Codable, Sendable, Equatable {
    /// Take the lease even if another client holds it ("Take over").
    public let force: Bool

    public init(force: Bool) { self.force = force }
}

/// One entry of `connectedClients` in `GET /device`.
public struct ConnectedClient: Codable, Sendable, Equatable, Hashable {
    public let clientName: String
    public let lastSeenAt: Date

    public init(clientName: String, lastSeenAt: Date) {
        self.clientName = clientName
        self.lastSeenAt = lastSeenAt
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
    /// False when the console has no emulator for this system (NextUI then
    /// hides its folder). Its games can still be managed. A pak that does not
    /// send the field is taken as having the emulator.
    public let emulatorInstalled: Bool

    public init(
        id: String, label: String, folder: String, tag: String, gameCount: Int, emulatorInstalled: Bool = true
    ) {
        self.id = id
        self.label = label
        self.folder = folder
        self.tag = tag
        self.gameCount = gameCount
        self.emulatorInstalled = emulatorInstalled
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        label = try c.decode(String.self, forKey: .label)
        folder = try c.decode(String.self, forKey: .folder)
        tag = try c.decode(String.self, forKey: .tag)
        gameCount = try c.decode(Int.self, forKey: .gameCount)
        emulatorInstalled = try c.decodeIfPresent(Bool.self, forKey: .emulatorInstalled) ?? true
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
    /// The files `DELETE /games/{id}?keepSave=true` leaves in place: the
    /// save, its clock file and the states.
    public static func isKeptWithSave(_ role: GameFileRole) -> Bool {
        role == .save || role == .rtc || role == .state
    }
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
