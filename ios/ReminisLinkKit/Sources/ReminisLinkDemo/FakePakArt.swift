// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import CryptoKit
import Foundation
import ReminisLinkKit

// Milestone M8 of the fake pak: `PUT` and `DELETE /games/{gameId}/art`.
// A new art is kept in memory (its ETag is its SHA-256); a removed art
// "goes to the trash" (counted, not listed).

extension FakePak {
    /// Whether a game has an art now (changes made through the API first).
    func hasArt(gameId: String, game: DemoLibrary.Game) -> Bool {
        lock.withLock { customArt[gameId] != nil || (game.hasArt && !removedArt.contains(gameId)) }
    }

    /// The art of a game and its ETag, nil if it has none.
    func currentArt(gameId: String, game: DemoLibrary.Game) -> (data: Data, etag: String)? {
        lock.withLock {
            if let data = customArt[gameId] { return (data, Self.etag(of: data)) }
            if game.hasArt && !removedArt.contains(gameId) { return (Self.artPNG, Self.artETag) }
            return nil
        }
    }

    /// The arts replaced or removed (what went to the trash).
    public var trashedArtCount: Int { lock.withLock { _trashedArtCount } }

    static func etag(of data: Data) -> String {
        "\"sha256-" + SHA256.hash(data: data).prefix(8).map { String(format: "%02x", $0) }.joined() + "\""
    }

    func putArt(gameId: String, request: URLRequest, body: Data) -> FakeResponse {
        guard let (system, game) = library.game(id: gameId) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        guard body.count <= ArtImage.maxBytes else { return .error(413, code: "too_large", message: "Art larger than 4 MiB") }
        guard request.value(forHTTPHeaderField: "Content-Type")?.lowercased().hasPrefix("image/png") == true,
              let size = ArtImage.pngSize(body), size.width > 0, size.height > 0,
              size.width <= ArtImage.maxSide, size.height <= ArtImage.maxSide
        else { return .error(422, code: "unsupported_format", message: "Not a PNG of at most 1024 x 1024") }
        let had = hasArt(gameId: gameId, game: game)
        lock.withLock {
            if had { _trashedArtCount += 1 }
            customArt[gameId] = body
            removedArt.remove(gameId)
        }
        return .json(200, summaryJSON(system: system, game: game))
    }

    func deleteArt(gameId: String) -> FakeResponse {
        guard let (system, game) = library.game(id: gameId) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        guard hasArt(gameId: gameId, game: game) else { return .error(404, code: "not_found", message: "No art") }
        lock.withLock {
            _trashedArtCount += 1
            customArt[gameId] = nil
            removedArt.insert(gameId)
        }
        return .json(200, summaryJSON(system: system, game: game))
    }
}
#endif
