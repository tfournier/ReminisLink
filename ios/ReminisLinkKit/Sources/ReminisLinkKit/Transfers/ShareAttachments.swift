import Foundation
import UniformTypeIdentifiers

/// The attachments of a share, as the share extension reads them
/// (docs/specs/06-app-ios.md §4.8): only real files are copied. A web link
/// is never kept, whatever its representation (`public.url`, or a text
/// holding only a URL): the console never downloads a link (removed from
/// v1). The decision is made on the content, not on the declared types: an
/// attachment that offers a file (data, or a file URL) is read as a file,
/// and dropped only when what it holds is a lone URL. One attachment that
/// cannot be read never stops the others. Files are copied by the file
/// system, never loaded in memory (except a file that starts like a link,
/// read to check that it holds nothing else).
public enum ShareAttachments {
    /// What a share held.
    public struct Result: Sendable, Equatable {
        /// Copies of the files, each in a folder of its own.
        public var files: [URL] = []
        /// Attachments that were web links (dropped).
        public var links = 0
        /// Attachments that could not be read.
        public var failures = 0
    }

    /// A text file larger than this is a file, never checked for a link
    /// (no URL is that long; a cheat file is at most 5 MiB).
    static let maxLinkCheckBytes: Int64 = 8 << 20

    /// Copies the files of `providers` into `directory`, on the caller's
    /// actor (`NSItemProvider` is not `Sendable`).
    nonisolated(nonsending) public static func copyFiles(from providers: [NSItemProvider], into directory: URL) async -> Result {
        var result = Result()
        for provider in providers {
            do {
                switch try await copy(provider, into: directory) {
                case .file(let url): result.files.append(url)
                case .link: result.links += 1
                case .nothing: break
                }
            } catch {
                result.failures += 1
            }
        }
        return result
    }

    enum Item: Equatable {
        case file(URL)
        case link
        case nothing
    }

    /// The representation to read as a file: data that is neither text nor
    /// a URL first, then text (a `.cht` may only be offered as text). The
    /// URL types are not files (`public.url` conforms to `public.data`).
    static func fileType(_ types: [UTType]) -> UTType? {
        types.first { $0.conforms(to: .data) && !$0.conforms(to: .text) && !$0.conforms(to: .url) }
            ?? types.first { $0.conforms(to: .data) && !$0.conforms(to: .url) }
    }

    /// The text is a lone `http`, `https` or `ftp` link: once trimmed, a
    /// single URL without whitespace, whatever its length.
    public static func isLinkText(_ text: String) -> Bool {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !trimmed.contains(where: \.isWhitespace),
              let url = URL(string: trimmed), let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https" || scheme == "ftp"
        else { return false }
        return url.host() != nil
    }

    /// The readings of a file as UTF-8 or UTF-16 (with a byte order mark,
    /// or decodable little- or big-endian); empty when it is none of them.
    static func decodeText(_ data: Data) -> [String] {
        if data.starts(with: [0xFF, 0xFE]) || data.starts(with: [0xFE, 0xFF]) {
            return String(data: data, encoding: .utf16).map { [$0] } ?? []
        }
        var texts: [String] = []
        if let utf8 = String(data: data, encoding: .utf8) { texts.append(utf8) }
        if data.count % 2 == 0 {
            for encoding in [String.Encoding.utf16LittleEndian, .utf16BigEndian] {
                if let text = String(data: data, encoding: encoding) { texts.append(text) }
            }
        }
        return texts
    }

    /// The first bytes may start a link: once the byte order mark, the NUL
    /// bytes of UTF-16 and leading whitespace are left out, they begin with
    /// `http://`, `https://` or `ftp://`. Checked before reading a whole
    /// file, so a ROM is never loaded in memory.
    static func mayStartWithLink(_ prefix: Data) -> Bool {
        var bytes = Array(prefix)
        if bytes.starts(with: [0xEF, 0xBB, 0xBF]) { bytes.removeFirst(3) }
        if bytes.starts(with: [0xFF, 0xFE]) || bytes.starts(with: [0xFE, 0xFF]) { bytes.removeFirst(2) }
        let ascii = bytes.filter { $0 != 0 }.drop { $0 == 0x20 || (0x09...0x0D).contains($0) }
        let start = String(decoding: ascii.prefix(8), as: UTF8.self).lowercased()
        return ["http://", "https://", "ftp://"].contains { start.hasPrefix($0) }
    }

    /// A file that holds only a link, in any of the encodings above.
    static func isLinkFile(_ url: URL) -> Bool {
        guard let size = try? FileDigest.size(of: url), size > 0, size <= maxLinkCheckBytes,
              let handle = try? FileHandle(forReadingFrom: url)
        else { return false }
        defer { try? handle.close() }
        guard let prefix = try? handle.read(upToCount: 64), mayStartWithLink(prefix),
              let data = try? Data(contentsOf: url)
        else { return false }
        return decodeText(data).contains(where: isLinkText)
    }

    nonisolated(nonsending) static func copy(_ provider: NSItemProvider, into directory: URL) async throws -> Item {
        let types = provider.registeredTypeIdentifiers.compactMap { UTType($0) }
        if let type = fileType(types) {
            // A file representation: read it, then look at what it holds.
            let name = provider.suggestedName
            let copied: URL? = try await withCheckedThrowingContinuation { continuation in
                _ = provider.loadFileRepresentation(forTypeIdentifier: type.identifier) { url, error in
                    // The provider deletes its file when this closure returns.
                    do {
                        guard let url else { throw error ?? CocoaError(.fileReadUnknown) }
                        continuation.resume(returning: try copyFile(url, name: name, into: directory))
                    } catch {
                        continuation.resume(throwing: error)
                    }
                }
            }
            guard let copied else { return .nothing }
            if isLinkFile(copied) {
                try? FileManager.default.removeItem(at: copied.deletingLastPathComponent())
                return .link
            }
            return .file(copied)
        }
        // Only URL types: a file URL is a file, anything else a link.
        guard types.contains(where: { $0.conforms(to: .url) }) else { return .nothing }
        let url: URL? = try await withCheckedThrowingContinuation { continuation in
            _ = provider.loadObject(ofClass: URL.self) { url, error in
                if let error { continuation.resume(throwing: error) } else { continuation.resume(returning: url) }
            }
        }
        guard let url else { return .nothing }
        guard url.isFileURL else { return .link }
        guard let copied = try copyFile(url, name: provider.suggestedName, into: directory) else { return .nothing }
        if isLinkFile(copied) {
            try? FileManager.default.removeItem(at: copied.deletingLastPathComponent())
            return .link
        }
        return .file(copied)
    }

    /// Copies a file under the suggested name (which gets the extension of
    /// the file when it has none: a `.cht` offered as text stays `.cht`),
    /// else its own name, in a new folder.
    static func copyFile(_ url: URL, name: String?, into directory: URL) throws -> URL? {
        var fileName = url.lastPathComponent
        if let name, !name.isEmpty {
            let ext = url.pathExtension
            let named = !(name as NSString).pathExtension.isEmpty
            fileName = ext.isEmpty || named ? name : name + "." + ext
        }
        let folder = directory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let destination = folder.appending(path: fileName.replacingOccurrences(of: "/", with: "_"))
        let scoped = url.startAccessingSecurityScopedResource()
        defer { if scoped { url.stopAccessingSecurityScopedResource() } }
        try FileManager.default.copyItem(at: url, to: destination)
        return destination
    }
}
