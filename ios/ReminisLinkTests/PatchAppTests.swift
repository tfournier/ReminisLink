import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M6 in the app: the Patch flow from Files, a link and the SD
/// card, new file and replacement, the errors, cancellation, and a shared
/// patch opening the Patch screen.
@MainActor
@Suite("Patch in the app", .serialized)
struct PatchAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-m6-\(UUID().uuidString)")
    let skyGarden = DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest.gba")
    let skyGardenGame = PatchGame(
        id: DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest.gba"), name: "Sky Garden Quest",
        fileName: "Sky Garden Quest.gba", kind: .file, systemLabel: "Game Boy Advance"
    )
    /// The size of Sky Garden Quest in the demo library.
    let skyGardenSize = 16_777_216
    /// Keeps the app models alive for the whole test: a session only
    /// holds its app `unowned`, as the app outlives every session.
    let keeper = Keeper()

    final class Keeper {
        var apps: [AppModel] = []
    }

    func makePak() -> FakePak {
        let pak = FakePak(host: "m6-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func makeApp(_ pak: FakePak, pair: Bool = true) async throws -> AppModel {
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
        keeper.apps.append(app)
        guard pair else { return app }
        let clientId = try app.store.installationClientId()
        let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "Test")
        let key = try await APIClient(endpoint: pak.endpoint, session: pak.makeSession()).pair(attempt)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: clientId, key: key)
        return app
    }

    func picked(_ app: AppModel, name: String, contents: Data) async throws -> PickedFile {
        let directory = root.appending(path: "src-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let source = directory.appending(path: name)
        try contents.write(to: source)
        return try await PickedFile.copy(source, into: app.transferDirectory)
    }

    /// Waits for the app side (upload, requests), then polls like the
    /// screen does until the flow ends.
    func runToTheEnd(_ flow: PatchFlow) async {
        for _ in 0..<500 {
            switch flow.step {
            case .preparing, .sending, .starting:
                try? await Task.sleep(for: .milliseconds(10))
            case .downloading, .patching:
                await flow.tick()
            case .idle, .succeeded, .failed, .cancelled:
                return
            }
        }
    }

    /// Waits until the flow reaches a step.
    func wait(_ flow: PatchFlow, until done: (PatchFlow.Step) -> Bool) async {
        for _ in 0..<500 where !done(flow.step) {
            try? await Task.sleep(for: .milliseconds(10))
        }
    }

    func waitUntilGone(_ url: URL) async {
        for _ in 0..<50 where FileManager.default.fileExists(atPath: url.path) {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    @Test func aPatchFromFilesMakesANewGame() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let flow = PatchFlow(session: session, game: skyGardenGame)
        #expect(flow.fileName == "Sky Garden Quest (patched).gba")
        #expect(!flow.canStart)
        let bps = DemoPatch.bps(sourceSize: skyGardenSize, sourceCRC32: 1, target: Data(count: 64))
        let file = try await picked(app, name: "translation.bps", contents: bps)
        flow.file = file
        flow.fileName = "Sky Garden Quest (FR).gba"
        #expect(flow.canStart)
        let revision = session.libraryRevision
        flow.start()
        await runToTheEnd(flow)
        let produced = DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest (FR).gba")
        #expect(flow.step == .succeeded(gameId: produced))
        // Sent to the staging area, then patched by its stagingId.
        #expect(pak.log.contains { $0.method == "POST" && $0.path.hasSuffix("/uploads") })
        #expect(pak.log.contains { $0.method == "POST" && $0.path.hasSuffix("/patch") && $0.status == 202 })
        // The job is also on the Transfers screen.
        let item = try #require(session.transfers.jobs.first)
        #expect(item.kind == .patch)
        #expect(item.title == String(localized: "Patch: Sky Garden Quest"))
        #expect(session.libraryRevision > revision)
        // The copy of the file is gone once on the console.
        await waitUntilGone(file.directory)
        #expect(!FileManager.default.fileExists(atPath: file.directory.path))
    }

    @Test func aPatchForAnotherROMSaysSoAndIsNotSentTwice() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let flow = PatchFlow(session: app.session(for: pak.deviceId), game: skyGardenGame)
        flow.file = try await picked(app, name: "other.bps", contents: DemoPatch.bps(sourceSize: 1234, sourceCRC32: 1, target: Data(count: 8)))
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.patchMismatch))
        #expect(AppError.patchMismatch.message
            == String(localized: "This patch does not match this ROM. Check the ROM version the patch asks for."))
        // Another try (here a replacement) reuses the staged file.
        flow.mode = .replace
        #expect(flow.step == .idle)
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.patchMismatch))
        #expect(pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/uploads") }.count == 1)
    }

    @Test func aFileThatIsNotAPatchIsUnsupported() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let flow = PatchFlow(session: app.session(for: pak.deviceId), game: skyGardenGame)
        flow.file = try await picked(app, name: "notes.ips", contents: Data("hello".utf8))
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.unsupportedPatch))
        #expect(AppError.unsupportedPatch.message.contains("IPS, BPS or UPS"))
    }

    @Test func aLinkIsDownloadedByTheConsoleThenApplied() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .link
        flow.linkText = "not a link"
        #expect(!flow.canStart)
        flow.linkText = "https://example.org/files/hack.bps"
        #expect(flow.canStart)
        flow.start()
        await wait(flow) { $0 == .downloading }
        #expect(session.transfers.jobs.first?.kind == .download)
        await runToTheEnd(flow)
        #expect(flow.step == .succeeded(gameId: DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest (patched).gba")))
        #expect(session.transfers.jobs.map(\.kind) == [.download, .patch])
    }

    @Test func aLinkTheConsoleCannotDownloadSaysSo() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let flow = PatchFlow(session: app.session(for: pak.deviceId), game: skyGardenGame)
        flow.source = .link
        flow.linkText = "https://example.org/missing"
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.patchDownloadFailed))
        #expect(!pak.log.contains { $0.path.hasSuffix("/patch") })
    }

    @Test func aPatchOfTheSDCardReplacesTheGame() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let sorted = SdFile.sortedForGame(files, fileName: "Sky Garden Quest.gba", name: "Sky Garden Quest")
        #expect(sorted.first?.fileName == "Sky Garden Quest (Translation).bps")
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = sorted.first?.id
        flow.mode = .replace
        // The name does not matter for a replacement.
        flow.fileName = ""
        #expect(flow.canStart)
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .succeeded(gameId: skyGarden))
        #expect(!pak.log.contains { $0.path.hasSuffix("/uploads") })
        let trash = try await session.perform { try await $0.trash() }
        #expect(trash.entries.first?.reason == "replace-rom")
    }

    @Test func aRunningPatchCanBeCancelled() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        await wait(flow) { $0 == .patching }
        await flow.tick()
        await flow.cancel()
        #expect(flow.step == .cancelled)
        #expect(pak.log.contains { $0.method == "DELETE" && $0.path.contains("/jobs/") })
        // A new choice starts again from scratch.
        flow.mode = .replace
        #expect(flow.step == .idle)
    }

    @Test func aLostJobIsReported() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        await wait(flow) { $0 == .patching }
        // The pak restarts: it forgets its jobs (and this client).
        let clientId = try #require(try app.store.credentials(for: pak.deviceId)?.clientId)
        let key = try #require(try app.store.credentials(for: pak.deviceId)?.key)
        pak.restart()
        pak.addClient(id: clientId, key: key)
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.jobLost))
    }

    @Test func whatCanBeStarted() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let multiDisc = PatchFlow(session: session, game: PatchGame(
            id: "g", name: "Starlight Saga", fileName: "Starlight Saga", kind: .folderM3U, systemLabel: "Sony PlayStation"
        ))
        multiDisc.source = .link
        multiDisc.linkText = "https://example.org/a.ips"
        #expect(!multiDisc.isPatchable && !multiDisc.canStart)

        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .link
        flow.linkText = "https://example.org/a.ips"
        flow.fileName = "../x.gba"
        #expect(!flow.canStart)
        flow.fileName = "Sky Garden Quest (FR).gb"
        #expect(flow.canStart && flow.changesExtension)
        flow.mode = .replace
        #expect(!flow.changesExtension)
        // No game yet (a shared patch).
        let shared = PatchFlow(session: session, game: nil)
        shared.source = .link
        shared.linkText = "https://example.org/a.ips"
        #expect(!shared.canStart)
        shared.game = skyGardenGame
        #expect(shared.fileName == "Sky Garden Quest (patched).gba")
        #expect(shared.canStart)
    }

    @Test func aSharedPatchOpensThePatchScreen() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let directory = root.appending(path: "share-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let patch = directory.appending(path: "Hack.bps")
        try Data("BPS1".utf8).write(to: patch)
        let request = try app.shareInbox.write(files: [patch], urls: [])
        app.checkShareInbox()
        guard case .patch(let context) = app.sheet else { Issue.record("no patch sheet"); return }
        #expect(context.request?.id == request.id)
        #expect(context.deviceId == pak.deviceId)
        #expect(context.game == nil)
        // Opened once only.
        app.checkShareInbox()
        guard case .patch(let again) = app.sheet else { Issue.record("sheet replaced"); return }
        #expect(again.id == context.id)
        // Closing the sheet consumes the request.
        app.sheet = nil
        app.sheetDismissed()
        #expect(app.shareInbox.pending().isEmpty)

        // A ROM still opens the Add screen.
        let rom = directory.appending(path: "Homebrew.gba")
        try Data(count: 4).write(to: rom)
        try app.shareInbox.write(files: [rom], urls: [])
        app.checkShareInbox()
        guard case .add = app.sheet else { Issue.record("no add sheet"); return }
    }

    @Test func patchErrorTexts() {
        let mismatch = ServerError(status: 0, code: .patchMismatch, message: "")
        let format = ServerError(status: 422, code: .unsupportedFormat, message: "")
        let full = ServerError(status: 507, code: .insufficientStorage, message: "")
        let missing = ServerError(status: 0, code: .notFound, message: "")
        #expect(AppError.patch(APIError.server(mismatch)) == .patchMismatch)
        #expect(AppError.patch(APIError.server(format)) == .unsupportedPatch)
        #expect(AppError.patch(APIError.server(full)) == .api(.server(full)))
        #expect(AppError.patch(CancellationError()) == .cancelled)
        #expect(AppError.patchJob(mismatch, kind: .patch) == .patchMismatch)
        #expect(AppError.patchJob(missing, kind: .download) == .patchDownloadFailed)
        #expect(AppError.patchJob(full, kind: .download) == .api(.server(full)))
        #expect(AppError.jobLost.action == nil && AppError.patchMismatch.action == nil)
    }
}
