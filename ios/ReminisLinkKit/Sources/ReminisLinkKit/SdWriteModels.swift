import Foundation

// Codable models of the writes of the SD card browser and of the renaming
// of a game (milestone M23, protocol/openapi.yaml: POST /sd/folders,
// POST /sd/trash, POST /games/{gameId}/rename).

/// Why a place of the SD card cannot be changed (`SdProtection`, the
/// `details.protected` of a `409 conflict`; open: an unknown value is kept
/// and shown as a generic refusal).
public struct SdProtection: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public init(from decoder: Decoder) throws {
        rawValue = try decoder.singleValueContainer().decode(String.self)
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(rawValue)
    }

    /// A folder the OS needs (read only with everything in it).
    public static let system = SdProtection(rawValue: "system")
    /// ReminisLink itself (its data, its own pak).
    public static let data = SdProtection(rawValue: "data")
    /// A main folder of the OS, the root of the card, or a folder holding a
    /// protected place: never moved to the trash.
    public static let root = SdProtection(rawValue: "root")
    /// A folder holding a symbolic link or a special file.
    public static let link = SdProtection(rawValue: "link")
}

/// `POST /sd/folders`.
public struct SdFolderRequest: Encodable, Sendable, Equatable {
    /// Nil for the root of the card (sent as `null`).
    public let parentId: String?
    public let name: String

    public init(parentId: String?, name: String) {
        self.parentId = parentId
        self.name = name
    }

    private enum CodingKeys: String, CodingKey { case parentId, name }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        if let parentId { try c.encode(parentId, forKey: .parentId) } else { try c.encodeNil(forKey: .parentId) }
        try c.encode(name, forKey: .name)
    }
}

/// An entry of `POST /sd/trash`, with the version the app showed (its
/// size, date and opaque version): the pak refuses it (`409
/// details.changed`) when the entry on the card is no longer that one.
public struct SdTrashItem: Encodable, Sendable, Equatable {
    public let sdEntryId: String
    public let sizeBytes: Int64?
    public let modifiedAt: Date
    public let version: String?

    public init(entry: SdEntry) {
        sdEntryId = entry.sdEntryId
        sizeBytes = entry.sizeBytes
        modifiedAt = entry.modifiedAt
        version = entry.version
    }

    private enum CodingKeys: String, CodingKey { case sdEntryId, sizeBytes, modifiedAt, version }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(sdEntryId, forKey: .sdEntryId)
        // Required, null for a folder.
        if let sizeBytes { try c.encode(sizeBytes, forKey: .sizeBytes) } else { try c.encodeNil(forKey: .sizeBytes) }
        try c.encode(modifiedAt, forKey: .modifiedAt)
        try c.encodeIfPresent(version, forKey: .version)
    }
}

/// `POST /sd/trash`: 1 to 100 entries in one trash entry, all or nothing.
public struct SdTrashRequest: Encodable, Sendable, Equatable {
    public let items: [SdTrashItem]

    public init(entries: [SdEntry]) {
        var seen = Set<String>()
        items = entries.filter { seen.insert($0.sdEntryId).inserted }.map(SdTrashItem.init(entry:))
    }

    /// The most entries of one request: the app never splits a selection
    /// (one request stays all or nothing).
    public static let maxIds = 100
}

/// `POST /games/{gameId}/rename`.
public struct GameRenameRequest: Codable, Sendable, Equatable {
    /// The new name without the extension.
    public let name: String
    public init(name: String) { self.name = name }
}

/// A file renamed (informative paths, never sent back).
public struct RenamedFile: Codable, Sendable, Equatable, Hashable {
    public let from: String
    public let to: String

    public init(from: String, to: String) {
        self.from = from
        self.to = to
    }
}

/// `200` of `POST /games/{gameId}/rename`: the game under its new name
/// (a new `id`) and what was renamed.
public struct GameRenameResult: Codable, Sendable, Equatable {
    public let game: GameSummary
    public let renamed: [RenamedFile]

    public init(game: GameSummary, renamed: [RenamedFile]) {
        self.game = game
        self.renamed = renamed
    }
}

extension ServerError {
    /// M23: `409 conflict` for a protected place of the SD card
    /// (`details.protected`), nil for another error.
    public var sdProtection: SdProtection? {
        guard code == .conflict, case .string(let raw)? = details["protected"] else { return nil }
        return SdProtection(rawValue: raw)
    }

    /// M23: `409 conflict` for a taken name of the SD browser: the free
    /// name `onConflict: rename` would give (`details.freeName`), nil when
    /// none or another error.
    public var sdFreeName: String? {
        guard code == .conflict, case .string(let name)? = details["freeName"] else { return nil }
        return name
    }

    /// M23: the entries of `POST /sd/trash` that changed since they were
    /// listed (`details.changed`), empty for another error.
    public var sdChanged: [String] {
        guard code == .conflict, case .array(let items)? = details["changed"] else { return [] }
        return items.compactMap { if case .string(let s) = $0 { s } else { nil } }
    }

    /// M23: the taken places of a game rename (`details.conflicts`).
    public var renameConflicts: [String] {
        guard code == .conflict, case .array(let items)? = details["conflicts"] else { return [] }
        return items.compactMap { if case .string(let s) = $0 { s } else { nil } }
    }

    /// The `404` of a route the pak does not have (an older pak): its
    /// catch-all answer, message `No route`.
    public var isMissingRoute: Bool {
        status == 404 && code == .notFound && message == "No route"
    }

    /// M23: a folder game whose discs another playlist lists cannot be
    /// renamed (`details.shared`).
    public var renameSharesDiscs: Bool {
        code == .conflict && details["shared"] == .bool(true)
    }
}

/// The rules of a new game name, as the pak checks them (M23): the app
/// says what is wrong before sending. The pak stays the judge.
public enum GameNameRule {
    /// What is wrong with a name.
    public enum Problem: Equatable, Sendable {
        case empty
        /// One of `/ \ : * ? " < > |`.
        case forbiddenCharacter(Character)
        /// A control or invisible format character.
        case invisibleCharacter
        case startsWithDot
        /// Ends with a space or a dot (exFAT drops them).
        case trailingSpaceOrDot
        /// A name Windows reserves (CON, NUL, COM1…).
        case reserved
        /// Longer than 255 bytes with the extension (or the data file
        /// names built on it).
        case tooLong
    }

    /// The characters exFAT forbids, and the path separators.
    public static let forbidden: Set<Character> = ["/", "\\", ":", "*", "?", "\"", "<", ">", "|"]

    /// The name as sent (NFC, as the pak stores it).
    public static func normalized(_ name: String) -> String {
        name.precomposedStringWithCanonicalMapping
    }

    /// The first problem of `name` for a game whose ROM has extension
    /// `fileExtension` (with its dot, "" for a folder game), nil when the
    /// pak should accept it. `longestSuffix` is the longest suffix the pak
    /// adds to build a data file name (`.gba.cht.bak` for NextUI).
    public static func problem(_ name: String, fileExtension: String = "", longestSuffix: Int = 16) -> Problem? {
        let n = normalized(name)
        if n.trimmingCharacters(in: .whitespaces).isEmpty { return .empty }
        if let c = n.first(where: { forbidden.contains($0) }) { return .forbiddenCharacter(c) }
        for scalar in n.unicodeScalars {
            let v = scalar.value
            if v < 0x20 || v == 0x7f || (0x80..<0xa0).contains(v) { return .invisibleCharacter }
            if scalar.properties.generalCategory == .format && v != 0x200c && v != 0x200d && !(0xe0020...0xe007f).contains(v) {
                return .invisibleCharacter
            }
        }
        if n.hasPrefix(".") { return .startsWithDot }
        if n.hasSuffix(" ") || n.hasSuffix(".") { return .trailingSpaceOrDot }
        if isReserved(n) { return .reserved }
        if n.utf8.count + fileExtension.utf8.count + longestSuffix > 255 { return .tooLong }
        return nil
    }

    private static func isReserved(_ name: String) -> Bool {
        let base = String(name.split(separator: ".", maxSplits: 1, omittingEmptySubsequences: false).first ?? "")
            .uppercased().trimmingCharacters(in: .whitespaces)
        if ["CON", "PRN", "AUX", "NUL"].contains(base) { return true }
        if base.count == 4, base.hasPrefix("COM") || base.hasPrefix("LPT"), let last = base.last, ("1"..."9").contains(last) {
            return true
        }
        return false
    }

    /// The name of a game without the extension of its ROM file (the
    /// editable part): the folder name of a folder game.
    public static func editableName(fileName: String, isFolder: Bool) -> String {
        guard !isFolder, let dot = fileName.lastIndex(of: "."), dot != fileName.startIndex else { return fileName }
        return String(fileName[..<dot])
    }

    /// The extension kept by a rename ("" for a folder game).
    public static func keptExtension(fileName: String, isFolder: Bool) -> String {
        guard !isFolder, let dot = fileName.lastIndex(of: "."), dot != fileName.startIndex else { return "" }
        return String(fileName[dot...])
    }
}
