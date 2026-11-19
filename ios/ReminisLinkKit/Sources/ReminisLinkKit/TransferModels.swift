import Foundation

// Codable models of the transfer routes of protocol/openapi.yaml:
// `/uploads`, `/downloads`, `/jobs` and `DELETE /games/{gameId}`.

/// What to do when the destination exists (`OnConflict`).
public enum OnConflict: String, Codable, Sendable, CaseIterable {
    /// `409 conflict` (the default).
    case fail
    /// Adds ` (2)`, ` (3)`... to the name.
    case rename
    /// The old file goes to the trash.
    case overwrite
}

/// Destination of an upload or a download (`UploadTarget` / `DownloadTarget`).
///
/// `fileName` is required for an upload to `rom` or `staging`, optional for
/// a download (the pak then takes it from `Content-Disposition` or the URL).
public enum TransferTarget: Sendable, Hashable, Encodable {
    case rom(systemId: String, folderId: String? = nil, fileName: String?)
    case save(gameId: String)
    case state(gameId: String, slot: Int)
    case staging(fileName: String?)

    public var kind: String {
        switch self {
        case .rom: "rom"
        case .save: "save"
        case .state: "state"
        case .staging: "staging"
        }
    }

    public var fileName: String? {
        switch self {
        case .rom(_, _, let name), .staging(let name): name
        case .save, .state: nil
        }
    }

    /// The same target with another file name (rom and staging only).
    public func withFileName(_ name: String?) -> TransferTarget {
        switch self {
        case .rom(let systemId, let folderId, _): .rom(systemId: systemId, folderId: folderId, fileName: name)
        case .staging: .staging(fileName: name)
        case .save, .state: self
        }
    }

    private enum CodingKeys: String, CodingKey {
        case kind, systemId, folderId, fileName, gameId, slot
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(kind, forKey: .kind)
        switch self {
        case .rom(let systemId, let folderId, let fileName):
            try c.encode(systemId, forKey: .systemId)
            try c.encodeIfPresent(folderId, forKey: .folderId)
            try c.encodeIfPresent(fileName, forKey: .fileName)
        case .save(let gameId):
            try c.encode(gameId, forKey: .gameId)
        case .state(let gameId, let slot):
            try c.encode(gameId, forKey: .gameId)
            try c.encode(slot, forKey: .slot)
        case .staging(let fileName):
            try c.encodeIfPresent(fileName, forKey: .fileName)
        }
    }
}

/// `POST /uploads`.
public struct CreateUploadRequest: Encodable, Sendable, Equatable {
    public let target: TransferTarget
    public let sizeBytes: Int64
    public let sha256: String
    public let onConflict: OnConflict

    public init(target: TransferTarget, sizeBytes: Int64, sha256: String, onConflict: OnConflict = .fail) {
        self.target = target
        self.sizeBytes = sizeBytes
        self.sha256 = sha256
        self.onConflict = onConflict
    }
}

/// `201` of `POST /uploads`.
public struct UploadCreated: Codable, Sendable, Equatable {
    public let uploadId: String
    public let chunkSize: Int64
    public let offset: Int64
    public let expiresAt: Date

    public init(uploadId: String, chunkSize: Int64, offset: Int64, expiresAt: Date) {
        self.uploadId = uploadId
        self.chunkSize = chunkSize
        self.offset = offset
        self.expiresAt = expiresAt
    }
}

/// `GET /uploads/{uploadId}`.
public struct UploadStatus: Codable, Sendable, Equatable {
    public let uploadId: String
    public let offset: Int64
    public let sizeBytes: Int64

    public init(uploadId: String, offset: Int64, sizeBytes: Int64) {
        self.uploadId = uploadId
        self.offset = offset
        self.sizeBytes = sizeBytes
    }
}

/// `200` of `POST /uploads/{uploadId}/complete`, depending on the target:
/// `gameId` (rom), `backup` (save: the copy of the previous save, null if
/// there was none), `slot` (state) or `stagingId` (staging).
public struct UploadResult: Codable, Sendable, Equatable {
    public let gameId: String?
    public let stagingId: String?
    public let backup: SaveBackup?
    public let slot: StateSlot?

    public init(gameId: String? = nil, stagingId: String? = nil, backup: SaveBackup? = nil, slot: StateSlot? = nil) {
        self.gameId = gameId
        self.stagingId = stagingId
        self.backup = backup
        self.slot = slot
    }
}

/// `POST /downloads`.
public struct DownloadRequest: Encodable, Sendable, Equatable {
    public let url: String
    public let target: TransferTarget
    public let sha256: String?
    public let onConflict: OnConflict

    public init(url: String, target: TransferTarget, sha256: String? = nil, onConflict: OnConflict = .rename) {
        self.url = url
        self.target = target
        self.sha256 = sha256
        self.onConflict = onConflict
    }
}

/// `202` of `POST /downloads` and `POST /games/{gameId}/patch`.
public struct JobAccepted: Codable, Sendable, Equatable {
    public let jobId: String
    public init(jobId: String) { self.jobId = jobId }
}

/// `download` or `patch` (open: an unknown kind is kept).
public struct JobKind: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let download = JobKind(rawValue: "download")
    public static let patch = JobKind(rawValue: "patch")
}

/// State of a job (open: an unknown state is kept).
public struct JobState: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let queued = JobState(rawValue: "queued")
    public static let running = JobState(rawValue: "running")
    public static let succeeded = JobState(rawValue: "succeeded")
    public static let failed = JobState(rawValue: "failed")
    public static let cancelled = JobState(rawValue: "cancelled")

    /// Whether the job will not change any more. An unknown state is taken
    /// as finished, so the app never polls forever.
    public var isFinished: Bool {
        self != .queued && self != .running
    }
}

/// A long operation on the device (docs/specs/03-protocole.md §8).
public struct Job: Codable, Sendable, Equatable, Identifiable {
    public struct Progress: Codable, Sendable, Equatable {
        public let done: Int64
        /// Nil when unknown.
        public let total: Int64?

        public init(done: Int64, total: Int64?) {
            self.done = done
            self.total = total
        }

        /// 0...1, nil when the total is unknown.
        public var fraction: Double? {
            guard let total, total > 0 else { return nil }
            return min(1, Double(done) / Double(total))
        }
    }

    public let id: String
    public let kind: JobKind
    public let state: JobState
    public let progress: Progress
    public let createdAt: Date
    public let updatedAt: Date
    /// The created resource when `succeeded`, e.g. `{ "gameId": "..." }`.
    public let result: [String: JSONValue]?
    /// When `failed`: the same object as an error response.
    public let error: ErrorEnvelope.Body?
    /// `DELETE /jobs/{jobId}` arrived while the job was running (M6): it
    /// stays `running` until its real end. Nil from an older pak.
    public let cancelRequested: Bool?

    public init(
        id: String, kind: JobKind, state: JobState, progress: Progress, createdAt: Date, updatedAt: Date,
        result: [String: JSONValue]? = nil, error: ErrorEnvelope.Body? = nil, cancelRequested: Bool? = nil
    ) {
        self.id = id
        self.kind = kind
        self.state = state
        self.progress = progress
        self.createdAt = createdAt
        self.updatedAt = updatedAt
        self.result = result
        self.error = error
        self.cancelRequested = cancelRequested
    }

    /// `result.gameId`, if any.
    public var resultGameId: String? {
        if case .string(let id) = result?["gameId"] { return id }
        return nil
    }

    /// The error of a failed job, as a server error (status 0: it did not
    /// come with an HTTP status).
    public var serverError: ServerError? {
        guard let error else { return nil }
        return ServerError(status: 0, code: ErrorCode(rawValue: error.code), message: error.message, details: error.details ?? [:])
    }
}

/// `GET /jobs`.
public struct JobList: Codable, Sendable, Equatable {
    public let jobs: [Job]
    public init(jobs: [Job]) { self.jobs = jobs }
}

/// `200` of `DELETE /games/{gameId}`.
/// A file of ``DeletionPreview``.
public struct DeletionItem: Codable, Sendable, Equatable, Hashable {
    /// Informative, never absolute: relative to the system folder under
    /// `Roms/`, to the SD card otherwise, `ReminisLink/...` for the copies
    /// of the pak.
    public let name: String
    /// A ``GameFileRole`` raw value, or `other`.
    public let role: GameFileRole
    public let size: Int64

    public init(name: String, role: GameFileRole, size: Int64) {
        self.name = name
        self.role = role
        self.size = size
    }
}

/// `GET /games/{gameId}/deletion` (M8): exactly what `DELETE /games/{id}`
/// would move and keep now, with the same `keepSave`.
public struct DeletionPreview: Codable, Sendable, Equatable {
    public let moved: [DeletionItem]
    /// Left in place on purpose: discs another playlist still lists, and
    /// with `keepSave` the save, the states and their copies.
    public let kept: [DeletionItem]
    public let movedBytes: Int64

    public init(moved: [DeletionItem], kept: [DeletionItem], movedBytes: Int64) {
        self.moved = moved
        self.kept = kept
        self.movedBytes = movedBytes
    }

    /// The copies the pak keeps of the game (locks, resume history): role
    /// `other`, names starting with `ReminisLink/`.
    public static func isPakCopy(_ item: DeletionItem) -> Bool {
        item.role == .other && item.name.hasPrefix("ReminisLink/")
    }

    /// Whether `keepSave` would keep something: read from a preview made
    /// with that option, any save, clock file, state or copy of the pak
    /// (locks, resume history) moved without it, or kept with it.
    public func hasDataKeptWithSave(keepSave: Bool) -> Bool {
        (keepSave ? kept : moved).contains { item in
            item.role == .save || item.role == .rtc || item.role == .state || Self.isPakCopy(item)
        }
    }
}

public struct TrashMoveResult: Codable, Sendable, Equatable {
    public let trashEntryId: String
    /// Original paths, relative to the SD card root (informative).
    public let moved: [String]
    public let movedBytes: Int64

    public init(trashEntryId: String, moved: [String], movedBytes: Int64) {
        self.trashEntryId = trashEntryId
        self.moved = moved
        self.movedBytes = movedBytes
    }
}

extension ServerError {
    /// For `409 conflict` on an upload chunk: the offset the device expects.
    public var expectedOffset: Int64? {
        guard code == .conflict else { return nil }
        return details["offset"]?.int64Value
    }
}
