import Foundation

// Codable models of milestone M12 (protocol/openapi.yaml): the tool and
// emulator paks of the console (`/paks`) and the read-only SD browser
// (`/sd/browse`). Every identifier is opaque: sent back as received.

/// `tool` (`Tools/<platform>/`) or `emu` (`Emus/<platform>/`). Open: an
/// unknown kind from a newer pak is kept.
public struct PakKind: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public init(from decoder: Decoder) throws {
        rawValue = try decoder.singleValueContainer().decode(String.self)
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(rawValue)
    }

    public static let tool = PakKind(rawValue: "tool")
    public static let emu = PakKind(rawValue: "emu")
}

/// Where to install a pak (`PakInstallKind`): `auto` lets the pak deduce
/// it from the archive, the SD folder or its `pak.json`.
public enum PakInstallKind: String, Codable, Sendable, CaseIterable {
    case auto
    case tool
    case emu
}

/// `onConflict` of `POST /paks/install`. The upload and download targets
/// use ``OnConflict`` (`fail` or `overwrite`) instead.
public enum PakConflictRule: String, Codable, Sendable {
    case fail
    /// The installed pak goes to the trash. Sent only after the user
    /// confirmed.
    case replace
}

/// An installed pak (`GET /paks`).
public struct Pak: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let pakId: String
    /// The folder name without `.pak`.
    public let name: String
    public let kind: PakKind
    /// Informative, relative to the SD card. Never sent back.
    public let path: String
    public let sizeBytes: Int64
    public let modifiedAt: Date
    /// `version` of its `pak.json`, nil when absent or unreadable.
    public let version: String?
    /// The pak of ReminisLink itself: never deleted nor replaced.
    public let isSelf: Bool

    public var id: String { pakId }

    public init(
        pakId: String, name: String, kind: PakKind, path: String, sizeBytes: Int64, modifiedAt: Date,
        version: String? = nil, isSelf: Bool = false
    ) {
        self.pakId = pakId
        self.name = name
        self.kind = kind
        self.path = path
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
        self.version = version
        self.isSelf = isSelf
    }

    private enum CodingKeys: String, CodingKey {
        case pakId, name, kind, path, sizeBytes, modifiedAt, version
        case isSelf = "self"
    }

    /// Lenient: `path`, `sizeBytes`, `version` and `self` may be missing
    /// (taken as empty, 0, nil, false); unknown fields are ignored.
    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        pakId = try c.decode(String.self, forKey: .pakId)
        name = try c.decode(String.self, forKey: .name)
        kind = try c.decode(PakKind.self, forKey: .kind)
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        sizeBytes = try c.decodeIfPresent(Int64.self, forKey: .sizeBytes) ?? 0
        modifiedAt = try c.decode(Date.self, forKey: .modifiedAt)
        version = try c.decodeIfPresent(String.self, forKey: .version)
        isSelf = try c.decodeIfPresent(Bool.self, forKey: .isSelf) ?? false
    }
}

/// `GET /paks`.
public struct PakList: Codable, Sendable, Equatable {
    public let paks: [Pak]
    public init(paks: [Pak]) { self.paks = paks }

    /// The tools, then the emulators, then any other kind, each by name
    /// ignoring case (the pak already sorts them so).
    public static func grouped(_ paks: [Pak]) -> [(kind: PakKind, paks: [Pak])] {
        var order: [PakKind] = [.tool, .emu]
        for pak in paks where !order.contains(pak.kind) { order.append(pak.kind) }
        return order.compactMap { kind in
            let group = paks.filter { $0.kind == kind }
                .sorted { $0.name.localizedCaseInsensitiveCompare($1.name) == .orderedAscending }
            return group.isEmpty ? nil : (kind, group)
        }
    }
}

/// `POST /paks/install`.
public struct PakInstallRequest: Encodable, Sendable, Equatable {
    public let sdEntryId: String
    public let kind: PakInstallKind
    public let onConflict: PakConflictRule

    public init(sdEntryId: String, kind: PakInstallKind = .auto, onConflict: PakConflictRule = .fail) {
        self.sdEntryId = sdEntryId
        self.kind = kind
        self.onConflict = onConflict
    }
}

/// `folder` or `file` (open: an unknown kind is kept, and neither opened
/// nor downloaded).
public struct SdEntryKind: RawRepresentable, Codable, Hashable, Sendable {
    public let rawValue: String
    public init(rawValue: String) { self.rawValue = rawValue }

    public init(from decoder: Decoder) throws {
        rawValue = try decoder.singleValueContainer().decode(String.self)
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        try c.encode(rawValue)
    }

    public static let folder = SdEntryKind(rawValue: "folder")
    public static let file = SdEntryKind(rawValue: "file")
}

/// A folder or file of the SD card (`GET /sd/browse`).
public struct SdEntry: Codable, Sendable, Equatable, Hashable, Identifiable {
    public let sdEntryId: String
    public let name: String
    public let kind: SdEntryKind
    /// Nil for a folder.
    public let sizeBytes: Int64?
    public let modifiedAt: Date

    public var id: String { sdEntryId }

    public init(sdEntryId: String, name: String, kind: SdEntryKind, sizeBytes: Int64?, modifiedAt: Date) {
        self.sdEntryId = sdEntryId
        self.name = name
        self.kind = kind
        self.sizeBytes = sizeBytes
        self.modifiedAt = modifiedAt
    }

    public var isFolder: Bool { kind == .folder }
    public var isFile: Bool { kind == .file }

    /// A folder whose name ends with `.pak` (ignoring case): installed as
    /// is by `POST /paks/install`.
    public var isPakFolder: Bool { isFolder && name.lowercased().hasSuffix(".pak") }

    /// A file whose name ends with `.zip` (ignoring case).
    public var isZip: Bool { isFile && name.lowercased().hasSuffix(".zip") }

    /// What the SD picker of "Install a pak" offers: a zip archive or a
    /// `.pak` folder (the pak decides whether it really is a pak).
    public var isInstallableAsPak: Bool { isZip || isPakFolder }
}

/// `GET /sd/browse`: one folder of the SD card.
public struct SdFolder: Codable, Sendable, Equatable {
    /// Nil for the root of the card.
    public let folderId: String?
    /// Informative ("" for the root). Never sent back.
    public let path: String
    /// The `folderId` of the parent, nil when the parent is the root (and
    /// for the root itself).
    public let parentId: String?
    public let entries: [SdEntry]
    /// More than 5000 entries: only the first 5000 are listed.
    public let truncated: Bool

    public init(folderId: String?, path: String, parentId: String?, entries: [SdEntry], truncated: Bool = false) {
        self.folderId = folderId
        self.path = path
        self.parentId = parentId
        self.entries = entries
        self.truncated = truncated
    }

    public var isRoot: Bool { folderId == nil }

    private enum CodingKeys: String, CodingKey {
        case folderId, path, parentId, entries, truncated
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        folderId = try c.decodeIfPresent(String.self, forKey: .folderId)
        path = try c.decodeIfPresent(String.self, forKey: .path) ?? ""
        parentId = try c.decodeIfPresent(String.self, forKey: .parentId)
        entries = try c.decode([SdEntry].self, forKey: .entries)
        truncated = try c.decodeIfPresent(Bool.self, forKey: .truncated) ?? false
    }
}

/// `paks` (M12): the installed paks changed; reload `GET /paks`. Its data
/// is an empty object (fields of a newer pak are ignored).
public struct EventPaks: Codable, Sendable, Equatable {
    public init() {}
}

/// Why the pak refused an archive or folder (`422 unsupported_format` in
/// the error of a pak installation job). The pak's `message` is English
/// ("Invalid pak: no launch.sh in the pak folder"); the reasons the app
/// knows get their own translated text, any other is ``other``.
public enum PakRefusal: Equatable, Sendable {
    /// Not a zip archive, or a damaged one.
    case notAnArchive
    /// No `<Name>.pak` folder nor a loose pak in the archive.
    case noPak
    /// No `launch.sh` in the pak folder.
    case noLaunchScript
    /// More than one pak in one archive.
    case severalPaks
    /// A symbolic link, an absolute path, `..`, a name the SD card does
    /// not allow, an entry outside the pak folder…: refused for safety.
    case unsafeEntry
    /// More than 10 000 entries or 1 GiB uncompressed, or compressed too
    /// much.
    case tooLarge
    /// Encrypted, or a compression method the pak cannot read.
    case unsupportedCompression
    /// No usable name for a loose archive.
    case noName
    /// A reason this app does not know: the pak's English text.
    case other(String)

    /// The reason in the pak's message (`Invalid pak: ` dropped).
    public static func reason(of message: String) -> String {
        let prefix = "Invalid pak: "
        return message.hasPrefix(prefix) ? String(message.dropFirst(prefix.count)) : message
    }

    public init(message: String) {
        let reason = Self.reason(of: message)
        let lower = reason.lowercased()
        func has(_ text: String) -> Bool { lower.contains(text) }
        if has("not a zip") || has("corrupt archive") || has("unreadable entry") {
            self = .notAnArchive
        } else if has("no launch.sh") {
            self = .noLaunchScript
        } else if has("more than one pak") {
            self = .severalPaks
        } else if has("no .pak folder") || has("pak folder is a file") || has("invalid pak folder name")
                    || has("not a .pak folder nor an archive") {
            self = .noPak
        } else if has("cannot name the pak") {
            self = .noName
        } else if has("encrypted") || has("compression method") {
            self = .unsupportedCompression
        } else if has("entries") && has("more than") || has("uncompressed") || has("compressed more than")
                    || has("central directory") {
            self = .tooLarge
        } else if has("symbolic link") || has("absolute") || has("outside the pak folder") || has("path component")
                    || has("backslash") || has("control character") || has("nul byte") || has("utf-8")
                    || has("same name") || has("neither a file nor a folder") || has("longer than")
                    || has("character the sd card") || has("empty entry name") {
            self = .unsafeEntry
        } else {
            self = .other(reason)
        }
    }
}

extension ServerError {
    /// `409 conflict` of a pak installation for an installed pak of the
    /// same name: its name, from `details.existing` (`Tools/tg5040/Clock.pak`
    /// gives `Clock`). Nil for ReminisLink itself (``isPakSelfConflict``).
    public var conflictingPakName: String? {
        guard code == .conflict, !isPakSelfConflict, case .string(let existing)? = details["existing"] else { return nil }
        let last = existing.split(separator: "/").last.map(String.init) ?? existing
        return last.lowercased().hasSuffix(".pak") ? String(last.dropLast(4)) : last
    }

    /// `409 conflict` with `details.self: true`: ReminisLink cannot delete
    /// or replace itself while it runs (M12).
    public var isPakSelfConflict: Bool {
        guard code == .conflict, case .bool(true)? = details["self"] else { return false }
        return true
    }

    /// `422 unsupported_format` of a pak installation: why.
    public var pakRefusal: PakRefusal? {
        code == .unsupportedFormat ? PakRefusal(message: message) : nil
    }
}

extension Job {
    /// `result.pakId` of a pak installation (a `pak-install` job, or a
    /// `download` job with a `pak` target).
    public var resultPakId: String? {
        if case .string(let id) = result?["pakId"] { return id }
        return nil
    }
}
