import Foundation

/// What the share extension hands to the app (docs/specs/06-app-ios.md §4.8).
public struct ShareRequest: Codable, Sendable, Equatable, Identifiable {
    public let id: UUID
    public let createdAt: Date
    /// File names inside the request's directory.
    public let files: [String]
    /// Web links (`http`/`https`).
    public let urls: [String]

    public init(id: UUID = UUID(), createdAt: Date = Date(), files: [String], urls: [String]) {
        self.id = id
        // Whole seconds, as encoded in request.json.
        self.createdAt = Date(timeIntervalSince1970: createdAt.timeIntervalSince1970.rounded(.down))
        self.files = files
        self.urls = urls
    }
}

/// The share requests stored in the app group container, one directory per
/// request: copies of the shared files and a `request.json` written last
/// (atomically), so the app never reads a half-written request.
///
/// The extension only copies files (by the file system, never through
/// memory) and never sends anything to a console.
public struct ShareInbox: Sendable {
    public static let urlHost = "add"
    public let root: URL

    public init(root: URL) {
        self.root = root
    }

    /// The inbox of the app group, or nil without a container.
    public static func appGroup(_ identifier: String) -> ShareInbox? {
        guard let container = FileManager.default.containerURL(forSecurityApplicationGroupIdentifier: identifier) else {
            return nil
        }
        return ShareInbox(root: container.appending(path: "ShareInbox", directoryHint: .isDirectory))
    }

    /// `reminislink://add?request=<id>`: opens the app on the Add screen.
    public static func url(for request: ShareRequest) -> URL {
        URL(string: "\(PairingLink.scheme)://\(urlHost)?request=\(request.id.uuidString.lowercased())")!
    }

    /// The request id of a `reminislink://add` link.
    public static func requestId(from url: URL) -> UUID? {
        guard url.scheme?.lowercased() == PairingLink.scheme, url.host()?.lowercased() == urlHost,
              let value = URLComponents(url: url, resolvingAgainstBaseURL: false)?
                .queryItems?.first(where: { $0.name == "request" })?.value
        else { return nil }
        return UUID(uuidString: value)
    }

    public func directory(for id: UUID) -> URL {
        root.appending(path: id.uuidString.lowercased(), directoryHint: .isDirectory)
    }

    public func fileURL(_ name: String, in request: ShareRequest) -> URL {
        directory(for: request.id).appending(path: name)
    }

    /// Stores a request: copies `files` (names made unique), then writes
    /// `request.json`. Links other than `http`/`https` are dropped.
    @discardableResult
    public func write(files: [URL], urls: [URL], id: UUID = UUID()) throws -> ShareRequest {
        let manager = FileManager.default
        let directory = directory(for: id)
        try manager.createDirectory(at: directory, withIntermediateDirectories: true)
        var names: [String] = []
        do {
            for file in files {
                let name = Self.uniqueName(Self.safeName(file.lastPathComponent), taken: Set(names))
                try manager.copyItem(at: file, to: directory.appending(path: name))
                names.append(name)
            }
            let links = urls.compactMap { DownloadURL.validated($0.absoluteString)?.absoluteString }
            let request = ShareRequest(id: id, files: names, urls: links)
            let data = try ProtocolJSON.encoder().encode(request)
            try data.write(to: directory.appending(path: "request.json"), options: .atomic)
            return request
        } catch {
            try? manager.removeItem(at: directory)
            throw error
        }
    }

    /// Complete requests, oldest first.
    public func pending() -> [ShareRequest] {
        let manager = FileManager.default
        guard let entries = try? manager.contentsOfDirectory(at: root, includingPropertiesForKeys: nil) else { return [] }
        return entries.compactMap { directory -> ShareRequest? in
            guard let data = try? Data(contentsOf: directory.appending(path: "request.json")) else { return nil }
            return try? ProtocolJSON.decoder().decode(ShareRequest.self, from: data)
        }
        .sorted { $0.createdAt < $1.createdAt }
    }

    public func request(id: UUID) -> ShareRequest? {
        pending().first { $0.id == id }
    }

    public func remove(_ id: UUID) {
        try? FileManager.default.removeItem(at: directory(for: id))
    }

    /// Removes requests older than `age` and directories without a
    /// `request.json` (an extension killed while copying).
    public func purge(olderThan age: TimeInterval = 7 * 86_400, now: Date = Date()) {
        let manager = FileManager.default
        guard let entries = try? manager.contentsOfDirectory(
            at: root, includingPropertiesForKeys: [.contentModificationDateKey]
        ) else { return }
        for directory in entries {
            let json = directory.appending(path: "request.json")
            if let data = try? Data(contentsOf: json),
               let request = try? ProtocolJSON.decoder().decode(ShareRequest.self, from: data) {
                if now.timeIntervalSince(request.createdAt) > age { try? manager.removeItem(at: directory) }
            } else {
                let modified = (try? directory.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate
                if now.timeIntervalSince(modified ?? .distantPast) > 3600 { try? manager.removeItem(at: directory) }
            }
        }
    }

    /// A file name without directory parts nor control characters.
    static func safeName(_ name: String) -> String {
        let cleaned = name.replacingOccurrences(of: "/", with: "_").replacingOccurrences(of: "\\", with: "_")
            .filter { !$0.unicodeScalars.contains { $0.value < 0x20 || $0.value == 0x7f } }
        let trimmed = cleaned.trimmingCharacters(in: .whitespaces)
        return trimmed.isEmpty || trimmed == "." || trimmed == ".." || trimmed == "request.json" ? "file" : trimmed
    }

    static func uniqueName(_ name: String, taken: Set<String>) -> String {
        guard taken.contains(name) else { return name }
        let ext = AddKind.fileExtension(name)
        let stem = ext.isEmpty ? name : String(name.dropLast(ext.count + 1))
        var n = 2
        while true {
            let candidate = ext.isEmpty ? "\(stem) (\(n))" : "\(stem) (\(n)).\(ext)"
            if !taken.contains(candidate) { return candidate }
            n += 1
        }
    }
}
