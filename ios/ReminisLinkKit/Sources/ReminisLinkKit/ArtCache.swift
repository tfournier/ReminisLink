import CryptoKit
import Foundation

/// Disk cache of game art, by device, `gameId` and `ETag`, in `Caches/`.
public struct ArtCache: Sendable {
    public let directory: URL

    public init(directory: URL) {
        self.directory = directory
    }

    /// `Caches/ReminisLink/Art`.
    public static func defaultDirectory() -> URL {
        URL.cachesDirectory.appending(path: "ReminisLink/Art", directoryHint: .isDirectory)
    }

    public struct Entry: Equatable, Sendable {
        public let data: Data
        public let etag: String?
    }

    public func entry(deviceId: String, gameId: String) -> Entry? {
        let base = baseURL(deviceId: deviceId, gameId: gameId)
        guard let data = try? Data(contentsOf: base.appendingPathExtension("png")) else { return nil }
        let etag = (try? Data(contentsOf: base.appendingPathExtension("etag"))).flatMap { String(data: $0, encoding: .utf8) }
        return Entry(data: data, etag: etag)
    }

    public func store(_ data: Data, etag: String?, deviceId: String, gameId: String) {
        let base = baseURL(deviceId: deviceId, gameId: gameId)
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try? data.write(to: base.appendingPathExtension("png"), options: .atomic)
        if let etag {
            try? Data(etag.utf8).write(to: base.appendingPathExtension("etag"), options: .atomic)
        } else {
            try? FileManager.default.removeItem(at: base.appendingPathExtension("etag"))
        }
    }

    /// Forgets the cached art of a game (it was changed or removed): the
    /// next load asks the pak without `If-None-Match`.
    public func invalidate(deviceId: String, gameId: String) {
        remove(deviceId: deviceId, gameId: gameId)
    }

    public func remove(deviceId: String, gameId: String) {
        let base = baseURL(deviceId: deviceId, gameId: gameId)
        try? FileManager.default.removeItem(at: base.appendingPathExtension("png"))
        try? FileManager.default.removeItem(at: base.appendingPathExtension("etag"))
    }

    /// The art of a game: the cached copy if the pak says it is still valid
    /// (`304`), otherwise the new one, stored. Nil if the game has none.
    public func load(gameId: String, client: APIClient) async throws -> Data? {
        guard let deviceId = client.expectedDeviceId else { return nil }
        let cached = entry(deviceId: deviceId, gameId: gameId)
        switch try await client.art(gameId: gameId, ifNoneMatch: cached?.etag) {
        case .notModified:
            return cached?.data
        case .image(let data, let etag):
            store(data, etag: etag, deviceId: deviceId, gameId: gameId)
            return data
        case .none:
            remove(deviceId: deviceId, gameId: gameId)
            return nil
        }
    }

    /// File names never contain an id: SHA-256 of device and game ids.
    private func baseURL(deviceId: String, gameId: String) -> URL {
        let digest = SHA256.hash(data: Data((deviceId + "/" + gameId).utf8)).map { String(format: "%02x", $0) }.joined()
        return directory.appending(path: digest, directoryHint: .notDirectory)
    }
}
