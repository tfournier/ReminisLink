import Foundation

// Restore from the trash (M18, `GET` and `POST /trash/{entryId}/restore`).

/// `onConflict` of a restore.
public enum TrashRestoreConflict: String, Codable, Sendable, Equatable {
    /// `409 conflict` when a place is taken (default).
    case fail
    /// Every item of the entry goes back under the same free name.
    case rename
    /// The save of an existing game (or its backup copy) replaces the
    /// current save, which is kept first (``TrashRestorePreview/replaceKeptAs``).
    case replace
}

/// Why no free name exists (``TrashRestorePreview/freeNameUnavailable``).
public enum TrashRestoreNoFreeName: Equatable, Sendable {
    /// Every " (N)" up to 999 is taken.
    case allTaken
    /// A folder on the way is a file.
    case pathBlocked
    case other(String)

    public init(rawValue: String) {
        switch rawValue {
        case "all-taken": self = .allTaken
        case "path-blocked": self = .pathBlocked
        default: self = .other(rawValue)
        }
    }
}

/// Where the current files go with ``TrashRestoreConflict/replace``.
public enum TrashReplaceKeptAs: Equatable, Sendable {
    /// A new trash entry of the console.
    case trash
    /// A "before-restore" backup copy of the save.
    case backup
    case other(String)

    public init(rawValue: String) {
        switch rawValue {
        case "trash": self = .trash
        case "backup": self = .backup
        default: self = .other(rawValue)
        }
    }
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
    /// entry has no conflict; nil when no free name exists.
    public let freeName: String?
    /// Where it goes with ``TrashRestoreConflict/replace`` (nil when the
    /// entry cannot replace).
    public let replaceName: String?

    public init(name: String, size: Int64, conflict: Bool, freeName: String?, replaceName: String? = nil) {
        self.name = name
        self.size = size
        self.conflict = conflict
        self.freeName = freeName
        self.replaceName = replaceName
    }

    private enum CodingKeys: String, CodingKey { case name, size, conflict, freeName, replaceName }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        name = try c.decode(String.self, forKey: .name)
        size = try c.decode(Int64.self, forKey: .size)
        conflict = try c.decode(Bool.self, forKey: .conflict)
        freeName = try c.decodeIfPresent(String.self, forKey: .freeName)
        replaceName = try c.decodeIfPresent(String.self, forKey: .replaceName)
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
    /// Raw `freeNameUnavailable`: no free name exists (see ``noFreeName``).
    public let freeNameUnavailable: String?
    /// ``TrashRestoreConflict/replace`` is possible.
    public let replaceable: Bool
    /// The current files `replace` would move out of the way first.
    public let replaceDisplaced: [String]
    /// Raw `replaceKeptAs` (see ``keptAs``).
    public let replaceKeptAs: String?

    /// Why no free name exists, nil when one does (or no conflict).
    public var noFreeName: TrashRestoreNoFreeName? { freeNameUnavailable.map(TrashRestoreNoFreeName.init(rawValue:)) }
    /// Where `replace` keeps the current files.
    public var keptAs: TrashReplaceKeptAs? { replaceKeptAs.map(TrashReplaceKeptAs.init(rawValue:)) }
    /// A conflict that a free name can settle.
    public var canRename: Bool { conflict && freeNameUnavailable == nil }

    public init(entryId: String, reason: String, label: String, conflict: Bool, items: [TrashRestoreItem], restoredBytes: Int64,
                freeNameUnavailable: String? = nil, replaceable: Bool = false, replaceDisplaced: [String] = [], replaceKeptAs: String? = nil) {
        self.entryId = entryId
        self.reason = reason
        self.label = label
        self.conflict = conflict
        self.items = items
        self.restoredBytes = restoredBytes
        self.freeNameUnavailable = freeNameUnavailable
        self.replaceable = replaceable
        self.replaceDisplaced = replaceDisplaced
        self.replaceKeptAs = replaceKeptAs
    }

    private enum CodingKeys: String, CodingKey {
        case entryId, reason, label, conflict, items, restoredBytes, freeNameUnavailable, replaceable, replaceDisplaced, replaceKeptAs
    }

    /// Lenient: a pak of the first M18 build has no rename and replace fields.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        entryId = try c.decode(String.self, forKey: .entryId)
        reason = try c.decode(String.self, forKey: .reason)
        label = try c.decode(String.self, forKey: .label)
        conflict = try c.decode(Bool.self, forKey: .conflict)
        items = try c.decode([TrashRestoreItem].self, forKey: .items)
        restoredBytes = try c.decode(Int64.self, forKey: .restoredBytes)
        freeNameUnavailable = try c.decodeIfPresent(String.self, forKey: .freeNameUnavailable)
        replaceable = try c.decodeIfPresent(Bool.self, forKey: .replaceable) ?? false
        replaceDisplaced = try c.decodeIfPresent([String].self, forKey: .replaceDisplaced) ?? []
        replaceKeptAs = try c.decodeIfPresent(String.self, forKey: .replaceKeptAs)
    }
}

/// `POST /trash/{entryId}/restore`.
public struct TrashRestoreResult: Codable, Sendable, Equatable {
    /// Where the user files went, relative to the SD card.
    public let restored: [String]
    public let restoredBytes: Int64
    /// The files went back under a free name.
    public let renamed: Bool
    /// With `replace`: the trash entry holding the current files moved
    /// away (nil when they were kept as a backup copy).
    public let replacedEntryId: String?
    /// With `replace`: those current files.
    public let replaced: [String]
    /// Raw `replacedKeptAs` (see ``keptAs``).
    public let replacedKeptAs: String?

    public var keptAs: TrashReplaceKeptAs? { replacedKeptAs.map(TrashReplaceKeptAs.init(rawValue:)) }

    public init(restored: [String], restoredBytes: Int64, renamed: Bool, replacedEntryId: String? = nil, replaced: [String] = [], replacedKeptAs: String? = nil) {
        self.restored = restored
        self.restoredBytes = restoredBytes
        self.renamed = renamed
        self.replacedEntryId = replacedEntryId
        self.replaced = replaced
        self.replacedKeptAs = replacedKeptAs
    }

    private enum CodingKeys: String, CodingKey { case restored, restoredBytes, renamed, replacedEntryId, replaced, replacedKeptAs }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        restored = try c.decode([String].self, forKey: .restored)
        restoredBytes = try c.decode(Int64.self, forKey: .restoredBytes)
        renamed = try c.decode(Bool.self, forKey: .renamed)
        replacedEntryId = try c.decodeIfPresent(String.self, forKey: .replacedEntryId)
        replaced = try c.decodeIfPresent([String].self, forKey: .replaced) ?? []
        replacedKeptAs = try c.decodeIfPresent(String.self, forKey: .replacedKeptAs)
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

    /// `409` of a restore under a free name when none exists.
    public var trashRestoreNoFreeName: TrashRestoreNoFreeName? {
        guard code == .conflict, case .string(let raw)? = details["freeNameUnavailable"] else { return nil }
        return TrashRestoreNoFreeName(rawValue: raw)
    }

    /// `422` of `replace`: `details.reason` (`not-a-save`, `game-missing`,
    /// `save-format`), nil for another error.
    public var trashReplaceRefusal: String? {
        guard code == .unsupportedFormat, case .string(let raw)? = details["reason"] else { return nil }
        return raw
    }
}
