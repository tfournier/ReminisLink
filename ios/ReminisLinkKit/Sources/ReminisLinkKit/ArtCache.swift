import CryptoKit
import Foundation

/// Disk cache of game art, by device, `gameId` and `ETag`, in `Caches/`.
///
/// The PNG and its `ETag` are one file, written atomically: a later `304`
/// can only confirm the PNG that came with that `ETag`. A fetch started
/// before ``invalidate(deviceId:gameId:)`` never stores its answer (it may
/// be the art before the change). Copies of a cache share these
/// generations: make one per directory.
public struct ArtCache: Sendable {
    public let directory: URL
    private let generations = Generations()

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
        guard let file = try? Data(contentsOf: entryURL(deviceId: deviceId, gameId: gameId)),
              let newline = file.firstIndex(of: 0x0A),
              let etag = String(data: file[file.startIndex..<newline], encoding: .utf8)
        else { return nil }
        return Entry(data: Data(file[file.index(after: newline)...]), etag: etag.isEmpty ? nil : etag)
    }

    public func store(_ data: Data, etag: String?, deviceId: String, gameId: String) {
        // An ETag never holds a line break (an HTTP header value).
        let tag = etag.flatMap { $0.contains(where: \.isNewline) ? nil : $0 } ?? ""
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try? (Data(tag.utf8) + Data([0x0A]) + data).write(to: entryURL(deviceId: deviceId, gameId: gameId), options: .atomic)
    }

    /// Forgets the cached art of a game (it was changed or removed): the
    /// next load asks the pak without `If-None-Match`, and a load already
    /// running does not store what it gets.
    public func invalidate(deviceId: String, gameId: String) {
        generations.bump(key(deviceId: deviceId, gameId: gameId)) {
            remove(deviceId: deviceId, gameId: gameId)
        }
    }

    public func remove(deviceId: String, gameId: String) {
        try? FileManager.default.removeItem(at: entryURL(deviceId: deviceId, gameId: gameId))
    }

    /// The art of a game: the cached copy if the pak says it is still valid
    /// (`304`), otherwise the new one, stored. Nil if the game has none.
    /// Throws `CancellationError` when the task is cancelled, even if the
    /// answer had already arrived.
    public func load(gameId: String, client: APIClient) async throws -> Data? {
        guard let deviceId = client.expectedDeviceId else { return nil }
        let fetch = startFetch(deviceId: deviceId, gameId: gameId)
        let response = try await client.art(gameId: gameId, ifNoneMatch: fetch.cached?.etag)
        // Cancelled while the answer was delivered: neither stored nor
        // returned (the caller no longer wants it).
        try Task.checkCancellation()
        return complete(fetch, with: response)
    }

    /// A load in flight: the generation of the game and the cached entry
    /// when it started.
    struct Fetch {
        let deviceId: String
        let gameId: String
        let generation: UInt64
        let cached: Entry?
    }

    func startFetch(deviceId: String, gameId: String) -> Fetch {
        generations.read(key(deviceId: deviceId, gameId: gameId)) { generation in
            Fetch(deviceId: deviceId, gameId: gameId, generation: generation,
                  cached: entry(deviceId: deviceId, gameId: gameId))
        }
    }

    /// Stores the answer of `fetch` unless the art was invalidated since it
    /// started, and returns the art it gives.
    func complete(_ fetch: Fetch, with response: ArtResponse) -> Data? {
        let (deviceId, gameId) = (fetch.deviceId, fetch.gameId)
        return generations.ifCurrent(key(deviceId: deviceId, gameId: gameId), fetch.generation) { current in
            switch response {
            case .notModified:
                return fetch.cached?.data
            case .image(let data, let etag):
                if current { store(data, etag: etag, deviceId: deviceId, gameId: gameId) }
                return data
            case .none:
                if current { remove(deviceId: deviceId, gameId: gameId) }
                return nil
            }
        }
    }

    private func key(deviceId: String, gameId: String) -> String { deviceId + "/" + gameId }

    private func entryURL(deviceId: String, gameId: String) -> URL {
        baseURL(deviceId: deviceId, gameId: gameId).appendingPathExtension("art")
    }

    /// File names never contain an id: SHA-256 of device and game ids.
    private func baseURL(deviceId: String, gameId: String) -> URL {
        let digest = SHA256.hash(data: Data((deviceId + "/" + gameId).utf8)).map { String(format: "%02x", $0) }.joined()
        return directory.appending(path: digest, directoryHint: .notDirectory)
    }
}

/// Invalidation counters of the art cache, by game. Each closure runs under
/// the lock, so a check and the write it guards cannot interleave with an
/// invalidation.
private final class Generations: @unchecked Sendable {
    private let lock = NSLock()
    private var values: [String: UInt64] = [:]

    func read<T>(_ key: String, _ body: (UInt64) -> T) -> T {
        lock.withLock { body(values[key, default: 0]) }
    }

    func bump(_ key: String, _ body: () -> Void) {
        lock.withLock {
            values[key, default: 0] &+= 1
            body()
        }
    }

    func ifCurrent<T>(_ key: String, _ generation: UInt64, _ body: (Bool) -> T) -> T {
        lock.withLock { body(values[key, default: 0] == generation) }
    }
}
