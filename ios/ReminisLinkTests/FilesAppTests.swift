import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M23 in the app: the writes of the Files tab (folders, files
/// sent through the transfer queue, items to the trash and back), the
/// rename of a game, and the errors they get.
@MainActor
@Suite("Files and rename in the app (M23)", .serialized)
struct FilesAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-files-\(UUID().uuidString)")

    /// Sessions hold their app unowned: the apps outlive the tests.
    static var kept: [AppModel] = []

    func makePak() -> FakePak {
        let pak = FakePak(host: "m23-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let settings = UserDefaults(suiteName: suite)!
        settings.set(false, forKey: AppModel.syncOnConnectKey)
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            shareInbox: ShareInbox(root: root.appending(path: "inbox")),
            transferDirectory: root.appending(path: "transfers"),
            exportDirectory: root.appending(path: "exports"),
            settings: settings,
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        Self.kept.append(app)
        try await app.pairDirectly(with: pak)
        return app
    }

    func waitUntil(timeout: Duration = .seconds(10), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
    }

    @Test func foldersFilesAndTheTrash() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let downloads = FakePak.sdEntryId("Downloads")

        let folder = try await session.createSdFolder(parentId: downloads, name: "Notes")
        #expect(folder.name == "Notes" && folder.deletable)
        await #expect(throws: AppError.sdNameTaken) {
            _ = try await session.createSdFolder(parentId: downloads, name: "Notes")
        }
        await #expect(throws: AppError.sdProtected(.system)) {
            _ = try await session.createSdFolder(parentId: FakePak.sdEntryId(".userdata"), name: "x")
        }

        // Two files chosen on the iPhone go through the transfer queue.
        let source = root.appending(path: "picked")
        try FileManager.default.createDirectory(at: source, withIntermediateDirectories: true)
        let a = source.appending(path: "a.txt")
        let b = source.appending(path: "b.bin")
        try Data("hello".utf8).write(to: a)
        try Data(repeating: 7, count: 4096).write(to: b)
        let queued = try await session.addSdFiles([a, b], folderId: folder.sdEntryId, folderTitle: "Notes", securityScoped: false)
        #expect(queued == 2)
        #expect(session.transfers.uploads.allSatisfy { if case .sdFile = $0.target { true } else { false } })
        await waitUntil { session.transfers.uploads.allSatisfy { $0.phase.isFinished } }
        let listed = try await session.perform { try await $0.browseSd(folderId: folder.sdEntryId) }
        #expect(listed.entries.map(\.name) == ["a.txt", "b.bin"])

        // The same name again: a conflict, then "Keep Both".
        _ = try await session.addSdFiles([a], folderId: folder.sdEntryId, folderTitle: "Notes", securityScoped: false)
        let again = try #require(session.transfers.uploads.last)
        await waitUntil { again.phase.isFinished }
        #expect((again.failure as? APIError)?.code == .conflict)
        await session.transfers.retry(again, onConflict: .rename)
        await waitUntil { again.phase.isFinished && again.failure == nil }
        let after = try await session.perform { try await $0.browseSd(folderId: folder.sdEntryId) }
        #expect(after.entries.map(\.name) == ["a (2).txt", "a.txt", "b.bin"])

        // To the trash, then restored.
        await #expect(throws: AppError.sdProtected(.root)) {
            try await session.trashSdEntries([SdEntry(sdEntryId: FakePak.sdEntryId("Roms"), name: "Roms", kind: .folder, sizeBytes: nil, modifiedAt: Date())])
        }
        let moved = try await session.trashSdEntries([folder])
        #expect(moved == 3)
        _ = await session.loadTrash()
        let entry = try #require(session.trash?.entries.first)
        #expect(entry.label == "Notes")
        _ = try await session.restoreTrashEntry(entry.id, onConflict: .fail)
        let back = try await session.perform { try await $0.browseSd(folderId: folder.sdEntryId) }
        #expect(back.entries.count == 3)
    }

    @Test func renamesAGame() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let system = try #require(pak.library.systems.first)
        let id = DemoLibrary.id(system.folder + "/Thistledown Isles.hab")
        let detail = try await session.gameDetail(id: id)
        #expect(detail.cheatCount == 12)

        let result = try await session.renameGame(id: id, name: "Thistledown Isles (Europe)")
        #expect(result.game.fileName == "Thistledown Isles (Europe).hab")
        let renamed = try await session.gameDetail(id: result.game.id)
        #expect(renamed.cheatCount == 12 && renamed.hasSave)

        await #expect(throws: AppError.renameTaken(["Roms/\(system.folder)/Brambleway Dash.hab"])) {
            _ = try await session.renameGame(id: result.game.id, name: "Brambleway Dash")
        }
        await #expect(throws: AppError.invalidGameName) {
            _ = try await session.renameGame(id: result.game.id, name: "a:b")
        }
        await #expect(throws: AppError.gameRemoved) {
            _ = try await session.renameGame(id: id, name: "Other")
        }
    }

    @Test func errorsOfAnOlderPak() {
        let missing = ServerError(status: 404, code: .notFound, message: "No route")
        #expect(AppError.sdWrite(APIError.server(missing)) == .filesNeedNewerPak)
        #expect(AppError.rename(APIError.server(missing)) == .filesNeedNewerPak)
        let gone = ServerError(status: 404, code: .notFound, message: "Entry not found", details: ["missing": .array([])])
        #expect(AppError.sdWrite(APIError.server(gone)) == .sdEntryGone)
        let shared = ServerError(status: 409, code: .conflict, message: "", details: ["shared": .bool(true)])
        #expect(AppError.rename(APIError.server(shared)) == .renameSharedDiscs)
        #expect(!AppError.sdProtected(.link).message.isEmpty && !AppError.gameNameProblem(.tooLong).message.isEmpty)
    }
}
