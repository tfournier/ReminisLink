import Foundation

// Restore from the trash (M18, `GET` and `POST /trash/{entryId}/restore`).

/// `onConflict` of a restore.
public enum TrashRestoreConflict: String, Codable, Sendable, Equatable {
    /// `409 conflict` when a place is taken (default).
    case fail
    /// Every item of the entry goes back under the same free name.
    case rename
}

/// Body of `POST /trash/{entryId}/restore`.
public struct TrashRestoreRequest: Codable, Sendable, Equatable {
    public let onConflict: TrashRestoreConflict
    public init(onConflict: TrashRestoreConflict) { self.onConflict = onConflict }
}

/// A user file of a restore preview.
public struct TrashRestoreItem: Codable, Sendable, Equatable, Hashable {
    /// Original place, relative to the SD card.
    public let name: String
    public let size: Int64
    /// The place is taken (same name ignoring case).
    public let conflict: Bool
    /// Where it goes with ``TrashRestoreConflict/rename``; `name` when the
    /// entry has no conflict.
    public let freeName: String

    public init(name: String, size: Int64, conflict: Bool, freeName: String) {
        self.name = name
        self.size = size
        self.conflict = conflict
        self.freeName = freeName
    }
}

/// `GET /trash/{entryId}/restore`: what a restore would put back.
public struct TrashRestorePreview: Codable, Sendable, Equatable {
    public let entryId: String
    public let reason: String
    public let label: String
    /// At least one place is taken (a ReminisLink copy included).
    public let conflict: Bool
    public let items: [TrashRestoreItem]
    public let restoredBytes: Int64

    public init(entryId: String, reason: String, label: String, conflict: Bool, items: [TrashRestoreItem], restoredBytes: Int64) {
        self.entryId = entryId
        self.reason = reason
        self.label = label
        self.conflict = conflict
        self.items = items
        self.restoredBytes = restoredBytes
    }
}

/// `POST /trash/{entryId}/restore`.
public struct TrashRestoreResult: Codable, Sendable, Equatable {
    /// Where the user files went, relative to the SD card.
    public let restored: [String]
    public let restoredBytes: Int64
    /// The files went back under a free name.
    public let renamed: Bool

    public init(restored: [String], restoredBytes: Int64, renamed: Bool) {
        self.restored = restored
        self.restoredBytes = restoredBytes
        self.renamed = renamed
    }
}

extension ServerError {
    /// `409 conflict` of a restore from the trash: the taken places
    /// (`details.conflicts`), nil for another error.
    public var trashRestoreConflicts: [String]? {
        guard code == .conflict, case .array(let values)? = details["conflicts"] else { return nil }
        return values.compactMap { value in
            if case .string(let s) = value { return s }
            return nil
        }
    }
}
