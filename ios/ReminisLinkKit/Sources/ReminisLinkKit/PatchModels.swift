import Foundation

// Models of `POST /games/{gameId}/patch` (protocol/openapi.yaml,
// `PatchRequest`) and the helpers of the Patch screen
// (docs/specs/06-app-ios.md §4.5, milestone M6).

/// Where the patch file is on the device.
public enum PatchSource: Sendable, Hashable, Encodable {
    /// A staging file, by the `stagingId` its upload or its download
    /// (`result.stagingId` of the job) returned.
    case staging(stagingId: String)
    /// A patch of the SD card (`id` of `GET /sd/files?type=patch`).
    case sd(sdFileId: String)

    private enum CodingKeys: String, CodingKey {
        case type, stagingId, sdFileId
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        switch self {
        case .staging(let id):
            try c.encode("staging", forKey: .type)
            try c.encode(id, forKey: .stagingId)
        case .sd(let id):
            try c.encode("sd", forKey: .type)
            try c.encode(id, forKey: .sdFileId)
        }
    }
}

/// `new-file` creates a new ROM next to the original; `replace` replaces
/// the ROM (the original goes to the trash of the console; the save,
/// states and cheats stay).
public enum PatchOutputMode: String, Codable, Sendable, CaseIterable {
    case newFile = "new-file"
    case replace
}

/// `output` of `PatchRequest`. `fileName` is only sent for `new-file`.
public struct PatchOutput: Encodable, Sendable, Equatable {
    public let mode: PatchOutputMode
    public let fileName: String?

    public init(mode: PatchOutputMode, fileName: String? = nil) {
        self.mode = mode
        self.fileName = mode == .newFile ? fileName : nil
    }

    private enum CodingKeys: String, CodingKey {
        case mode, fileName
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.container(keyedBy: CodingKeys.self)
        try c.encode(mode, forKey: .mode)
        try c.encodeIfPresent(fileName, forKey: .fileName)
    }
}

/// `POST /games/{gameId}/patch`.
public struct PatchRequest: Encodable, Sendable, Equatable {
    public let patch: PatchSource
    public let output: PatchOutput

    public init(patch: PatchSource, output: PatchOutput) {
        self.patch = patch
        self.output = output
    }
}

/// Names on the Patch screen.
public enum PatchNaming {
    /// `<name> (patched).<ext>` for a ROM file name (`<name> (patched)`
    /// without an extension), as the pak names a new file by default.
    public static func suggestedFileName(forROM fileName: String) -> String {
        let ext = AddKind.fileExtension(fileName)
        guard !ext.isEmpty else { return "\(fileName) (patched)" }
        let stem = fileName.dropLast(ext.count + 1)
        let originalExt = fileName.suffix(ext.count)
        return "\(stem) (patched).\(originalExt)"
    }

    /// The name typed for a new file, trimmed, or nil if the pak would
    /// refuse it (docs/specs/03-protocole.md §5): empty, a directory part,
    /// a control or Unicode format character, a leading dot, a name
    /// reserved on Windows, more than 255 bytes.
    public static func validatedFileName(_ text: String) -> String? {
        let name = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !name.hasPrefix("."), name.utf8.count <= 255,
              !name.contains("/"), !name.contains("\\"),
              !name.unicodeScalars.contains(where: { $0.value < 0x20 || $0.value == 0x7f || $0.properties.generalCategory == .format })
        else { return nil }
        let stem = name.split(separator: ".", maxSplits: 1, omittingEmptySubsequences: false).first.map(String.init) ?? name
        let reserved = Set(["CON", "PRN", "AUX", "NUL"] + (1...9).flatMap { ["COM\($0)", "LPT\($0)"] })
        guard !reserved.contains(stem.trimmingCharacters(in: .whitespaces).uppercased()) else { return nil }
        return name
    }

    /// Whether two file names have the same extension (ignoring case).
    public static func sameExtension(_ a: String, _ b: String) -> Bool {
        AddKind.fileExtension(a) == AddKind.fileExtension(b)
    }

    /// Whether a file name has the extension of a patch (`.ips`, `.bps`,
    /// `.ups`). The pak detects the format by its header; this only
    /// decides which screen a shared file opens.
    public static func isPatchFileName(_ name: String) -> Bool {
        AddKind.patchExtensions.contains(AddKind.fileExtension(name))
    }
}

extension GameKind {
    /// Only a single-file game can be patched: a multi-disc game (`m3u`,
    /// `folder-m3u`) or an unknown kind is not offered.
    public var isPatchable: Bool { self == .file }
}

extension Job {
    /// `result.stagingId` of a download to the staging area.
    public var resultStagingId: String? {
        if case .string(let id) = result?["stagingId"] { return id }
        return nil
    }
}

extension ShareRequest {
    /// One patch file and nothing else: the share opens the Patch screen
    /// (docs/specs/06-app-ios.md §4.8) instead of the Add screen.
    public var isSinglePatch: Bool {
        urls.isEmpty && files.count == 1 && files.allSatisfy(PatchNaming.isPatchFileName)
    }
}
