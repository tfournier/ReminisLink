import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M5 in the app: replacing a save (direct or through the
/// queue), importing a `.cht` from Files, downloads for the share sheet,
/// the Add screen types and the error texts.
@MainActor
@Suite("Saves, states and cheats in the app", .serialized)
struct GameDataAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-m5-\(UUID().uuidString)")
    let skyGarden = DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest.gba")
    let pixelRunner = DemoLibrary.id("Game Boy Advance (GBA)/Pixel Runner.gba")

    func makePak() -> FakePak {
        let pak = FakePak(host: "m5-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            shareInbox: ShareInbox(root: root.appending(path: "inbox")),
            transferDirectory: root.appending(path: "transfers"),
            exportDirectory: root.appending(path: "exports"),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        let clientId = try app.store.installationClientId()
        let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "Test")
        let key = try await APIClient(endpoint: pak.endpoint, session: pak.makeSession()).pair(attempt)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: clientId, key: key)
        return app
    }

    /// A file picked in Files, copied into the app like the screens do.
    func picked(_ app: AppModel, name: String, contents: Data) async throws -> PickedFile {
        let directory = root.appending(path: "src-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let source = directory.appending(path: name)
        try contents.write(to: source)
        return try await PickedFile.copy(source, into: app.transferDirectory)
    }

    func waitUntilGone(_ url: URL) async {
        for _ in 0..<50 where FileManager.default.fileExists(atPath: url.path) {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    @Test func aSmallSaveIsReplacedDirectlyAndItsCopyDeleted() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let file = try await picked(app, name: "Sky Garden Quest.gba.sav", contents: Data(repeating: 7, count: 10_000))
        let revision = session.libraryRevision
        let outcome = try await session.replaceSave(gameId: skyGarden, gameName: "Sky Garden Quest", with: file)
        guard case .replaced(let backup) = outcome else { Issue.record("not replaced directly"); return }
        #expect(backup?.reason == .beforeReplace)
        #expect(pak.log.contains { $0.method == "PUT" && $0.path.hasSuffix("/save") && $0.status == 200 })
        #expect(!pak.log.contains { $0.path.hasSuffix("/uploads") })
        #expect(session.libraryRevision == revision + 1)
        await waitUntilGone(file.directory)
        #expect(!FileManager.default.fileExists(atPath: file.directory.path))
    }

    @Test func aSaveOver64MiBGoesThroughTheQueue() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let file = try await picked(app, name: "Big.sav", contents: Data())
        let handle = try FileHandle(forWritingTo: file.url)
        try handle.truncate(atOffset: UInt64(APIClient.maxSaveBodyBytes + 1))
        try handle.close()
        let big = PickedFile(url: file.url, name: file.name, sizeBytes: APIClient.maxSaveBodyBytes + 1)
        let outcome = try await session.replaceSave(gameId: skyGarden, gameName: "Sky Garden Quest", with: big)
        guard case .queued(let item) = outcome else { Issue.record("not queued"); return }
        #expect(item.target == .save(gameId: skyGarden))
        #expect(item.destination == "Sky Garden Quest")
        await session.transfers.waitUntilIdle()
        #expect(item.phase == .done, "\(String(describing: item.failure))")
        #expect(item.result?.backup?.reason == .beforeReplace)
        #expect(!pak.log.contains { $0.method == "PUT" && $0.path.hasSuffix("/save") })
    }

    @Test func aChtFromFilesIsSentToStagingThenImported() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let file = try await picked(app, name: "pack.cht", contents: Data("cheat0_desc = \"From Files\"\ncheat0_code = \"3\"\n".utf8))
        let result = try await session.importCheats(gameId: pixelRunner, from: file, mode: .merge)
        #expect(result == CheatImportResult(imported: 1, total: 1))
        #expect(pak.log.contains { $0.method == "POST" && $0.path.hasSuffix("/uploads") })
        let cheats = try await session.perform { [pixelRunner] in try await $0.cheats(gameId: pixelRunner) }
        #expect(cheats.cheats.map(\.description) == ["From Files"])
        await waitUntilGone(file.directory)
        #expect(!FileManager.default.fileExists(atPath: file.directory.path))
    }

    @Test func aFileWithoutCheatIsUnsupported() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let file = try await picked(app, name: "notes.cht", contents: Data("hello\n".utf8))
        do {
            _ = try await session.importCheats(gameId: skyGarden, from: file, mode: .replace)
            Issue.record("imported")
        } catch let error as AppError {
            #expect(error == .api(.server(ServerError(status: 422, code: .unsupportedFormat, message: "No cheat found"))))
        }
    }

    @Test func downloadsLandInTheExportDirectoryAndAreDeleted() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let gameId = skyGarden
        let exported = try await session.export { try await $0.downloadState(gameId: gameId, slot: 1, into: $1) }
        #expect(exported.file.url.lastPathComponent == "Sky Garden Quest.gba.st1")
        #expect(exported.file.directory.deletingLastPathComponent().standardizedFileURL == app.exportDirectory.standardizedFileURL)
        ExportedFiles.remove(exported.file)
        await waitUntilGone(exported.file.directory)
        #expect(!FileManager.default.fileExists(atPath: exported.file.directory.path))
    }

    @Test func saveIsOfferedForFilesButNotForLinks() {
        #expect(AddKind.sendable.contains(.save))
        #expect(!AddKind.linkKinds.contains(.save))
        #expect(AddKind.suggested(forFileName: "Sky Garden Quest.gba.sav") == .save)
    }

    @Test func automaticCopiesAreNamedAfterTheirReason() {
        let date = Date(timeIntervalSince1970: 0)
        #expect(BackupRow.title(SaveBackup(id: "1", label: "Boss", createdAt: date, sizeBytes: 1, reason: .manual)) == "Boss")
        #expect(BackupRow.title(SaveBackup(id: "2", label: nil, createdAt: date, sizeBytes: 1, reason: .manual)) == String(localized: "Backup copy"))
        // The pak's label of automatic copies is not translated: the reason is shown.
        #expect(BackupRow.title(SaveBackup(id: "3", label: "Avant remplacement", createdAt: date, sizeBytes: 1, reason: .beforeReplace))
            == String(localized: "Before replacement"))
        #expect(BackupRow.title(SaveBackup(id: "4", label: nil, createdAt: date, sizeBytes: 1, reason: BackupReason(rawValue: "future")))
            == String(localized: "Automatic copy"))
    }

    @Test func errorTextsOfMilestone5() {
        let locked = AppError.api(.server(ServerError(status: 409, code: .stateLocked, message: "")))
        #expect(locked.message == String(localized: "This state is locked on the console. Unlock it first."))
        let changed = AppError.api(.server(ServerError(status: 412, code: .conflict, message: "")))
        #expect(changed.message == String(localized: "The list changed on the console. Reload it."))
        let exists = AppError.api(.server(ServerError(status: 409, code: .conflict, message: "")))
        #expect(exists.message == String(localized: "A file with this name already exists on the console."))
        let unsupported = AppError.api(.server(ServerError(status: 422, code: .unsupportedFormat, message: "")))
        #expect(unsupported.message == String(localized: "The console found nothing it can use in this file."))
    }
}
