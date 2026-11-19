import Foundation
import ReminisLinkKit

/// Where a new game art comes from.
enum ArtPicture: Sendable {
    /// Bytes of a picture (Photos).
    case data(Data)
    /// A picture file chosen in Files (security-scoped).
    case file(URL)
}

extension ConsoleSession {
    /// Converts a picture into the art the pak takes (``ArtImage``), off
    /// the main actor, then `PUT /games/{gameId}/art`. The previous art
    /// goes to the trash of the console.
    func setArt(gameId: String, from picture: ArtPicture) async throws {
        let png = try await Task.detached(priority: .userInitiated) { () throws -> Data in
            switch picture {
            case .data(let data):
                return try ArtImage.png(from: data)
            case .file(let url):
                let accessing = url.startAccessingSecurityScopedResource()
                defer { if accessing { url.stopAccessingSecurityScopedResource() } }
                return try ArtImage.png(from: url)
            }
        }.value
        _ = try await perform { try await $0.setArt(gameId: gameId, png: png) }
        artChanged(gameId: gameId)
    }

    /// `DELETE /games/{gameId}/art`: the art goes to the trash of the console.
    func removeArt(gameId: String) async throws {
        _ = try await perform { try await $0.deleteArt(gameId: gameId) }
        artChanged(gameId: gameId)
    }

    /// The cached art is stale: dropped, art views and the library reload.
    private func artChanged(gameId: String) {
        app.artCache.invalidate(deviceId: deviceId, gameId: gameId)
        artRevision += 1
        contentChanged()
    }
}
