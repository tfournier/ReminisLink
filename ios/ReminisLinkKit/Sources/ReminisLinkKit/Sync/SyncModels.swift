import Foundation

// Codable models of `GET /saves` (milestone M9, protocol/openapi.yaml).

/// `SaveClockFile`: the `.rtc` that belongs to a save.
public struct SaveClockFile: Codable, Sendable, Equatable, Hashable {
    /// Informative, relative to the SD card root. Never sent back.
    public let path: String
    public let sizeBytes: Int64
    /// From the console clock (informative).
    public let modifiedAt: Date
    public let sha256: String

    public init(path: String, sizeBytes: Int64, modifiedAt: Date, sha256: String) {
        self.path = path
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
        self.sha256 = sha256
    }
}

/// `SaveSummary`: the save of one game.
public struct SaveSummary: Codable, Sendable, Equatable, Hashable, Identifiable {
    public var id: String { gameId }
    public let gameId: String
    public let systemId: String
    public let gameName: String
    public let fileName: String
    /// Informative, relative to the SD card root. Never sent back.
    public let path: String
    public let sizeBytes: Int64
    /// From the console clock (informative): compare `sha256`.
    public let modifiedAt: Date
    public let sha256: String
    public let rtc: SaveClockFile?

    public init(
        gameId: String, systemId: String, gameName: String, fileName: String, path: String,
        sizeBytes: Int64, modifiedAt: Date, sha256: String, rtc: SaveClockFile?
    ) {
        self.gameId = gameId
        self.systemId = systemId
        self.gameName = gameName
        self.fileName = fileName
        self.path = path
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
        self.sha256 = sha256
        self.rtc = rtc
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(gameId, forKey: .gameId)
        try c.encode(systemId, forKey: .systemId)
        try c.encode(gameName, forKey: .gameName)
        try c.encode(fileName, forKey: .fileName)
        try c.encode(path, forKey: .path)
        try c.encode(sizeBytes, forKey: .sizeBytes)
        try c.encode(modifiedAt, forKey: .modifiedAt)
        try c.encode(sha256, forKey: .sha256)
        // Required by the contract, null when there is no clock file.
        try c.encode(rtc, forKey: .rtc)
    }
}

/// `GET /saves`.
public struct SaveList: Codable, Sendable, Equatable {
    public let saves: [SaveSummary]
    /// More than 10000 saves: the list is incomplete.
    public let truncated: Bool
    public let generatedAt: Date

    public init(saves: [SaveSummary], truncated: Bool, generatedAt: Date) {
        self.saves = saves
        self.truncated = truncated
        self.generatedAt = generatedAt
    }
}
