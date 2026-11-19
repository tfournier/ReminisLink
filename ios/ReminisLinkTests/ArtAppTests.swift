import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
import UIKit
@testable import ReminisLink

/// Milestone M8 in the app: the image of a game, changed from a picture or
/// removed, against the fake pak.
@MainActor
@Suite("Game art in the app", .serialized)
struct ArtAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-m8-art-\(UUID().uuidString)")
    let skyGarden = DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest.gba")
    let keeper = Keeper()

    final class Keeper {
        var apps: [AppModel] = []
    }

    func makeApp(_ pak: FakePak) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            transferDirectory: root.appending(path: "transfers"),
            exportDirectory: root.appending(path: "exports"),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        keeper.apps.append(app)
        let clientId = try app.store.installationClientId()
        let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "Test")
        let key = try await APIClient(endpoint: pak.endpoint, session: pak.makeSession()).pair(attempt)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: clientId, key: key)
        return app
    }

    func makePak() -> FakePak {
        let pak = FakePak(host: "m8-art-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    @Test func aNewImageIsSentAsAPNGAndTheCacheIsDropped() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let client = try await session.connectedClient()
        _ = try await app.artCache.load(gameId: skyGarden, client: client)
        #expect(app.artCache.entry(deviceId: pak.deviceId, gameId: skyGarden) != nil)
        let library = session.libraryRevision
        let art = session.artRevision

        try await session.setArt(gameId: skyGarden, from: .data(GameArtButton.testPicture()))
        #expect(app.artCache.entry(deviceId: pak.deviceId, gameId: skyGarden) == nil)
        #expect(session.artRevision == art + 1)
        #expect(session.libraryRevision == library + 1)
        let png = try #require(try await app.artCache.load(gameId: skyGarden, client: client))
        let size = try #require(ArtImage.pngSize(png))
        #expect(size.width == 1024 && size.height == 768)
        #expect(pak.trashedArtCount == 1)

        try await session.removeArt(gameId: skyGarden)
        #expect(try await app.artCache.load(gameId: skyGarden, client: client) == nil)
        #expect(session.artRevision == art + 2)
        let detail = try await client.game(id: skyGarden)
        #expect(!detail.files.contains { $0.role == .art })
    }

    @Test func aPictureThatCannotBeReadSendsNothing() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        await #expect(throws: AppError.unreadablePicture) {
            do {
                try await session.setArt(gameId: skyGarden, from: .data(Data("not a picture".utf8)))
            } catch {
                throw AppError(error)
            }
        }
        #expect(!pak.log.contains { $0.method == "PUT" && $0.path.hasSuffix("/art") })
        #expect(AppError.unreadablePicture.action == nil)
    }

    /// A picture file of Files is read from its URL.
    @Test func aPictureFileIsUsed() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let url = root.appending(path: "picture.jpg")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try GameArtButton.testPicture().write(to: url)
        try await app.session(for: pak.deviceId).setArt(gameId: skyGarden, from: .file(url))
        #expect(pak.log.contains { $0.method == "PUT" && $0.path.hasSuffix("/art") && $0.status == 200 })
    }
}
