import Foundation

// Codable models of the save, state and cheat routes of protocol/openapi.yaml
// (milestone M5): `/games/{gameId}/save...`, `/states...`,
// `/resume-history...`, `/cheats...` and `/sd/files`.

// MARK: - Saves

/// Why a save backup exists (`Backup.reason`; open: an unknown reason is kept).
public struct BackupReason: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public static let manual = BackupReason(rawValue: "manual")
    public static let beforeReplace = BackupReason(rawValue: "before-replace")
    public static let beforeRestore = BackupReason(rawValue: "before-restore")
    public static let beforeDelete = BackupReason(rawValue: "before-delete")
    public static let beforePatch = BackupReason(rawValue: "before-patch")
}

/// A backup copy of a save (`Backup`).
public struct SaveBackup: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let id: String
    /// At most 80 characters; nil for most automatic copies.
    public let label: String?
    public let createdAt: Date
    public let sizeBytes: Int64
    public let reason: BackupReason

    public init(id: String, label: String?, createdAt: Date, sizeBytes: Int64, reason: BackupReason) {
        self.id = id
        self.label = label
        self.createdAt = createdAt
        self.sizeBytes = sizeBytes
        self.reason = reason
    }
}

/// `GET /games/{gameId}/save/backups`: newest first.
public struct BackupList: Codable, Sendable, Equatable {
    public let backups: [SaveBackup]
    public init(backups: [SaveBackup]) { self.backups = backups }
}

/// `BackupResult`: the copy made before a replacement or a restoration,
/// nil when there was no save to copy.
public struct BackupResult: Codable, Sendable, Equatable {
    public let backup: SaveBackup?
    public init(backup: SaveBackup?) { self.backup = backup }
}

/// `POST /games/{gameId}/save/backups`.
public struct CreateBackupRequest: Codable, Sendable, Equatable {
    public let label: String?
    public init(label: String?) { self.label = label }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(label, forKey: .label)
    }
}

/// The label of a new backup copy (`CreateBackupRequest.label`).
public enum BackupLabel {
    /// At most 80 characters. Counted in Unicode scalars, the unit of the
    /// pak (Go runes), which is never fewer than the characters shown.
    public static let maxLength = 80

    /// Trimmed, cut to ``maxLength`` scalars without splitting a
    /// character; nil when empty (the label is optional).
    public static func normalized(_ text: String) -> String? {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        return limited(trimmed)
    }

    /// `text` cut to ``maxLength`` scalars (whole characters only), for a
    /// text field that must not grow past the limit.
    public static func limited(_ text: String) -> String {
        guard text.unicodeScalars.count > maxLength else { return text }
        var out = ""
        var count = 0
        for character in text {
            let n = character.unicodeScalars.count
            if count + n > maxLength { break }
            out.append(character)
            count += n
        }
        return out
    }
}

// MARK: - States

/// Lock state of a slot (`Slot.lockStatus`; open: an unknown value is kept).
public struct LockStatus: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    /// Same as the locked copy.
    public static let intact = LockStatus(rawValue: "intact")
    /// Overwritten in game and put back at the last start of the pak
    /// (reported once, by the next `GET /states`).
    public static let restored = LockStatus(rawValue: "restored")
    /// The state is gone; the locked copy is still there.
    public static let missing = LockStatus(rawValue: "missing")
    /// The state differs from the locked copy and was not restored.
    public static let modified = LockStatus(rawValue: "modified")
    /// The locked copy comes from another emulator core: it cannot be
    /// restored (`409 conflict`).
    public static let coreChanged = LockStatus(rawValue: "core-changed")

    /// The states this app knows; any other value is shown as unknown,
    /// never as a protected state.
    public var isKnown: Bool {
        [.intact, .restored, .missing, .modified, .coreChanged].contains(self)
    }
}

/// A state slot (`Slot`).
public struct StateSlot: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let slot: Int
    public let sizeBytes: Int64
    public let modifiedAt: Date
    public let hasThumbnail: Bool
    public let locked: Bool
    /// Nil when not locked.
    public let lockStatus: LockStatus?

    public init(slot: Int, sizeBytes: Int64, modifiedAt: Date, hasThumbnail: Bool, locked: Bool, lockStatus: LockStatus?) {
        self.slot = slot
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
        self.hasThumbnail = hasThumbnail
        self.locked = locked
        self.lockStatus = lockStatus
    }

    public var id: Int { slot }

    /// The state file is gone (only the locked copy is left).
    public var isMissing: Bool { lockStatus == .missing }
    /// Overwritten in game and restored at the last start of the pak.
    public var wasRestored: Bool { lockStatus == .restored }
    /// There is a state file to download.
    public var canDownload: Bool { !isMissing }
    /// The pak refuses to delete a locked slot (`409 state_locked`).
    public var canDelete: Bool { !locked && !isMissing }
    /// The locked copy can be put back (not when it comes from another
    /// emulator core).
    public var canRestore: Bool { locked && lockStatus != .coreChanged }
    /// The state holds the bytes of the locked copy: unlocking erases the
    /// copy. Otherwise (missing, modified, unknown...) unlocking moves the
    /// copy to the trash.
    public var matchesLockedCopy: Bool { lockStatus == .intact || lockStatus == .restored }

    /// The number NextUI shows: slots 0 to 7 are shown 1 to 8
    /// (docs/specs/04-api-v1.md, States).
    public var displayNumber: Int { slot + 1 }
}

/// `GET /games/{gameId}/states`.
public struct SlotList: Codable, Sendable, Equatable {
    public let slots: [StateSlot]
    /// Slot of the automatic resume state (9 on NextUI), if any.
    public let resumeSlot: Int?

    /// The slots of the user on NextUI (shown 1 to 8).
    public static let userSlotRange = 0...7

    public init(slots: [StateSlot], resumeSlot: Int?) {
        self.slots = slots
        self.resumeSlot = resumeSlot
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        slots = try c.decode([StateSlot].self, forKey: .slots)
        resumeSlot = try c.decodeIfPresent(Int.self, forKey: .resumeSlot)
    }

    /// The slots the user saves to (0 to 7, shown 1 to 8). Slot 8 (a hidden
    /// "default" state NextUI never writes) and the resume slot are not
    /// among them.
    public var userSlots: [StateSlot] {
        slots.filter { Self.userSlotRange.contains($0.slot) && $0.slot != resumeSlot }
    }

    /// The automatic resume state, if present.
    public var resume: StateSlot? {
        guard let resumeSlot else { return nil }
        return slots.first { $0.slot == resumeSlot }
    }

    /// The same list with one slot replaced (after lock, unlock, restore).
    public func replacing(_ slot: StateSlot) -> SlotList {
        var slots = slots
        if let index = slots.firstIndex(where: { $0.slot == slot.slot }) {
            slots[index] = slot
        } else {
            slots.append(slot)
            slots.sort { $0.slot < $1.slot }
        }
        return SlotList(slots: slots, resumeSlot: resumeSlot)
    }
}

/// One archived version of the resume state.
public struct ResumeHistoryEntry: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let id: String
    public let capturedAt: Date
    public let sizeBytes: Int64

    public init(id: String, capturedAt: Date, sizeBytes: Int64) {
        self.id = id
        self.capturedAt = capturedAt
        self.sizeBytes = sizeBytes
    }
}

/// `GET /games/{gameId}/resume-history`: newest first, at most 5.
public struct ResumeHistory: Codable, Sendable, Equatable {
    public let entries: [ResumeHistoryEntry]
    public init(entries: [ResumeHistoryEntry]) { self.entries = entries }
}

// MARK: - Cheats

/// One cheat of a `.cht` file (`Cheat`).
///
/// `extra` holds the keys of this cheat the pak does not interpret, without
/// their `cheat<index>_` prefix (`handler`); the app sends them back
/// untouched and the pak prefixes them with the cheat's new position. `id` is local (lists and edits) and is
/// neither encoded nor compared.
public struct Cheat: Codable, Sendable, Hashable, Identifiable {
    public let id: UUID
    /// Position in the file when read (renumbered by the pak on write).
    public var index: Int?
    public var description: String
    public var code: String
    public var enabled: Bool
    public var extra: [String: String]

    public init(id: UUID = UUID(), index: Int? = nil, description: String, code: String, enabled: Bool,
                extra: [String: String] = [:]) {
        self.id = id
        self.index = index
        self.description = description
        self.code = code
        self.enabled = enabled
        self.extra = extra
    }

    private enum CodingKeys: String, CodingKey {
        case index, description, code, enabled, extra
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = UUID()
        index = try c.decodeIfPresent(Int.self, forKey: .index)
        description = try c.decode(String.self, forKey: .description)
        code = try c.decode(String.self, forKey: .code)
        enabled = try c.decode(Bool.self, forKey: .enabled)
        extra = try c.decodeIfPresent([String: String].self, forKey: .extra) ?? [:]
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encodeIfPresent(index, forKey: .index)
        try c.encode(description, forKey: .description)
        try c.encode(code, forKey: .code)
        try c.encode(enabled, forKey: .enabled)
        try c.encode(extra, forKey: .extra)
    }

    /// Same content (the local `id` is ignored).
    public static func == (a: Cheat, b: Cheat) -> Bool {
        a.index == b.index && a.description == b.description && a.code == b.code
            && a.enabled == b.enabled && a.extra == b.extra
    }

    public func hash(into hasher: inout Hasher) {
        hasher.combine(index)
        hasher.combine(description)
        hasher.combine(code)
        hasher.combine(enabled)
        hasher.combine(extra)
    }
}

/// `GET` and `PUT /games/{gameId}/cheats`.
public struct CheatList: Codable, Sendable, Equatable {
    public let cheats: [Cheat]
    /// Sent back in `If-Match` to replace the list.
    public let etag: String

    public init(cheats: [Cheat], etag: String) {
        self.cheats = cheats
        self.etag = etag
    }
}

/// Body of `PUT /games/{gameId}/cheats`: the whole list, in order.
public struct CheatsUpdate: Codable, Sendable, Equatable {
    public let cheats: [Cheat]
    public init(cheats: [Cheat]) { self.cheats = cheats }
}

/// Where imported cheats come from.
public enum CheatImportSource: Sendable, Equatable, Encodable {
    /// Downloaded by the console (5 MiB at most).
    case url(String)
    /// A `.cht` of the SD card (`id` of `GET /sd/files?type=cht`).
    case sd(sdFileId: String)
    /// The text of a `.cht` (1 MiB at most).
    case inline(String)
    /// A completed upload to the staging area (`target.kind = "staging"`),
    /// by its upload id (only until the pak restarts).
    case upload(uploadId: String)
    /// A staging file, by the `stagingId` its upload returned (valid 24 h;
    /// preferred to `upload`).
    case staging(stagingId: String)

    private enum CodingKeys: String, CodingKey {
        case type, url, sdFileId, text, uploadId, stagingId
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .url(let url):
            try c.encode("url", forKey: .type)
            try c.encode(url, forKey: .url)
        case .sd(let id):
            try c.encode("sd", forKey: .type)
            try c.encode(id, forKey: .sdFileId)
        case .inline(let text):
            try c.encode("inline", forKey: .type)
            try c.encode(text, forKey: .text)
        case .upload(let id):
            try c.encode("upload", forKey: .type)
            try c.encode(id, forKey: .uploadId)
        case .staging(let id):
            try c.encode("staging", forKey: .type)
            try c.encode(id, forKey: .stagingId)
        }
    }
}

/// `merge` adds the imported cheats after the current ones; `replace`
/// replaces the list.
public enum CheatImportMode: String, Codable, Sendable, CaseIterable {
    case merge
    case replace
}

/// `POST /games/{gameId}/cheats/import`.
public struct CheatImportRequest: Encodable, Sendable, Equatable {
    public let source: CheatImportSource
    public let mode: CheatImportMode

    public init(source: CheatImportSource, mode: CheatImportMode) {
        self.source = source
        self.mode = mode
    }
}

/// `200` of `POST /games/{gameId}/cheats/import`.
public struct CheatImportResult: Codable, Sendable, Equatable {
    public let imported: Int
    public let total: Int
    public init(imported: Int, total: Int) {
        self.imported = imported
        self.total = total
    }
}

// MARK: - SD card files

/// `type` of `GET /sd/files`.
public enum SdFileType: String, Sendable, CaseIterable {
    case cht
    case patch
}

/// A usable file of the SD card. `path` is informative only.
public struct SdFile: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let id: String
    public let path: String
    public let sizeBytes: Int64

    public init(id: String, path: String, sizeBytes: Int64) {
        self.id = id
        self.path = path
        self.sizeBytes = sizeBytes
    }

    /// The last component of `path`.
    public var fileName: String {
        String(path.split(separator: "/").last ?? Substring(path))
    }
}

/// `GET /sd/files`.
public struct SdFileList: Codable, Sendable, Equatable {
    public let files: [SdFile]
    public init(files: [SdFile]) { self.files = files }
}

extension SdFile {
    /// Files whose name starts with the ROM file name or the game name
    /// first (the usual `.cht` naming), then by path.
    public static func sortedForGame(_ files: [SdFile], fileName: String, name: String) -> [SdFile] {
        func matches(_ file: SdFile) -> Bool {
            let n = file.fileName.lowercased()
            return n.hasPrefix(fileName.lowercased()) || n.hasPrefix(name.lowercased())
        }
        return files.sorted { a, b in
            let ma = matches(a), mb = matches(b)
            if ma != mb { return ma }
            return a.path.localizedStandardCompare(b.path) == .orderedAscending
        }
    }
}

// MARK: - Downloads

/// A file downloaded from the pak (a save, a backup copy, a state), in a
/// directory of its own under the temporary directory. The caller deletes
/// ``directory`` once done with it.
public struct DownloadedFile: Sendable, Equatable {
    public let url: URL
    public var directory: URL { url.deletingLastPathComponent() }

    public init(url: URL) { self.url = url }
}

/// The file name of a `Content-Disposition` header, made safe to use as a
/// local file name.
public enum ContentDisposition {
    /// `filename="<name>"` (or unquoted, or RFC 5987 `filename*=UTF-8''...`),
    /// reduced to its last path component, without control characters;
    /// nil when absent or unusable.
    public static func fileName(_ header: String?) -> String? {
        guard let header else { return nil }
        var plain: String?
        var extended: String?
        for part in splitParameters(header) {
            let trimmed = part.trimmingCharacters(in: .whitespaces)
            guard let eq = trimmed.firstIndex(of: "=") else { continue }
            let key = trimmed[..<eq].trimmingCharacters(in: .whitespaces).lowercased()
            var value = trimmed[trimmed.index(after: eq)...].trimmingCharacters(in: .whitespaces)
            if key == "filename*" {
                if let quote = value.range(of: "''") {
                    value = String(value[quote.upperBound...])
                }
                extended = value.removingPercentEncoding
            } else if key == "filename" {
                if value.count >= 2, value.hasPrefix("\""), value.hasSuffix("\"") {
                    value = String(value.dropFirst().dropLast())
                        .replacingOccurrences(of: "\\\"", with: "\"")
                        .replacingOccurrences(of: "\\\\", with: "\\")
                }
                plain = value
            }
        }
        return safe(extended) ?? safe(plain)
    }

    /// A local file name: last component of `/` and `\`, no control
    /// characters, not `.`/`..`, not hidden.
    public static func safe(_ name: String?) -> String? {
        guard let name else { return nil }
        let last = name.split(whereSeparator: { $0 == "/" || $0 == "\\" }).last.map(String.init) ?? ""
        let cleaned = String(String.UnicodeScalarView(last.precomposedStringWithCanonicalMapping.unicodeScalars.filter {
            // No C0 or C1 control, no invisible formatting character.
            $0.value >= 0x20 && !(0x7f...0x9f).contains($0.value) && !$0.properties.isDefaultIgnorableCodePoint
        })).trimmingCharacters(in: .whitespaces)
        guard !cleaned.isEmpty, cleaned != ".", cleaned != "..", !cleaned.hasPrefix("."),
              cleaned.utf8.count <= 255
        else { return nil }
        return cleaned
    }

    /// A local name for a file picked or shared: ``safe(_:)``, and a name
    /// that starts with "." (refused as hidden) becomes `file.<...>`, so it
    /// keeps its extension.
    public static func localName(_ name: String) -> String {
        safe(name) ?? safe("file" + name) ?? "file"
    }

    /// Splits on `;` outside double quotes.
    private static func splitParameters(_ header: String) -> [String] {
        var parts: [String] = []
        var current = ""
        var quoted = false
        var escaped = false
        for c in header {
            if escaped {
                current.append(c)
                escaped = false
                continue
            }
            if c == "\\" && quoted {
                current.append(c)
                escaped = true
                continue
            }
            if c == "\"" { quoted.toggle() }
            if c == ";" && !quoted {
                parts.append(current)
                current = ""
            } else {
                current.append(c)
            }
        }
        parts.append(current)
        return parts
    }
}
