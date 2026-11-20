import Foundation
import UniformTypeIdentifiers

/// The attachments of a share, as the share extension reads them
/// (docs/specs/06-app-ios.md §4.8): only real files are copied. A web link
/// is never kept, whatever its representation (`public.url`, or
/// `public.plain-text` holding a URL): the console never downloads a link
/// (removed from v1). One attachment that cannot be read never stops the
/// others. Files are copied by the file system, never loaded in memory
/// (except a short text, checked for a lone link).
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

    /// The longest text checked for a lone link; a longer text is a file.
    static let maxLinkTextBytes = 4096

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

    /// A web link only: a URL that is not a file, and no data but text (the
    /// text of the link). A file representation (`public.file-url`, or data
    /// that is not text) makes it a file.
    static func isWebLinkOnly(_ types: [UTType]) -> Bool {
        let webURL = types.contains { $0.conforms(to: .url) && !$0.conforms(to: .fileURL) }
        let fileURL = types.contains { $0.conforms(to: .fileURL) }
        // `public.url` itself conforms to `public.data`.
        let binary = types.contains { $0.conforms(to: .data) && !$0.conforms(to: .text) && !$0.conforms(to: .url) }
        return webURL && !fileURL && !binary
    }

    /// The representation to copy: data that is not text first, then text
    /// (a `.cht` may only be offered as text).
    static func fileType(_ types: [UTType]) -> UTType? {
        types.first { $0.conforms(to: .data) && !$0.conforms(to: .text) && !$0.conforms(to: .url) }
            ?? types.first { $0.conforms(to: .data) && !$0.conforms(to: .url) }
    }

    /// The text is a lone `http`, `https` or `ftp` link (a link shared as
    /// text).
    public static func isLinkText(_ text: String) -> Bool {
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, !trimmed.contains(where: \.isWhitespace),
              let url = URL(string: trimmed), let scheme = url.scheme?.lowercased(),
              scheme == "http" || scheme == "https" || scheme == "ftp"
        else { return false }
        return url.host() != nil
    }

    nonisolated(nonsending) static func copy(_ provider: NSItemProvider, into directory: URL) async throws -> Item {
        let types = provider.registeredTypeIdentifiers.compactMap { UTType($0) }
        if isWebLinkOnly(types) { return .link }
        guard let type = fileType(types) else {
            // A file URL without data: copy the file it names.
            guard types.contains(where: { $0.conforms(to: .fileURL) }) else { return .nothing }
            let url: URL? = try await withCheckedThrowingContinuation { continuation in
                _ = provider.loadObject(ofClass: URL.self) { url, error in
                    if let error { continuation.resume(throwing: error) } else { continuation.resume(returning: url) }
                }
            }
            guard let url, url.isFileURL else { return .link }
            return try copyFile(url, name: nil, into: directory).map(Item.file) ?? .nothing
        }
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
        if type.conforms(to: .text), isLinkFile(copied) {
            try? FileManager.default.removeItem(at: copied.deletingLastPathComponent())
            return .link
        }
        return .file(copied)
    }

    /// A short text file holding only a link.
    static func isLinkFile(_ url: URL) -> Bool {
        guard let size = try? FileDigest.size(of: url), size <= maxLinkTextBytes,
              let data = try? Data(contentsOf: url), let text = String(data: data, encoding: .utf8)
        else { return false }
        return isLinkText(text)
    }

    /// Copies a file under its original name (the suggested name keeps the
    /// extension of the file when it has none), in a new folder.
    static func copyFile(_ url: URL, name: String?, into directory: URL) throws -> URL? {
        var fileName = url.lastPathComponent
        if let name, !name.isEmpty {
            let ext = url.pathExtension
            fileName = ext.isEmpty || name.lowercased().hasSuffix("." + ext.lowercased()) ? name : name + "." + ext
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
