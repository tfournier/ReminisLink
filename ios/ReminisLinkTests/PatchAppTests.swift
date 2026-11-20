import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M6 in the app: the Patch flow from Files and the SD card, new
/// file and replacement, the errors, cancellation, and a shared
/// patch opening the Patch screen.
@MainActor
@Suite("Patch in the app", .serialized)
struct PatchAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-m6-\(UUID().uuidString)")
    let skyGarden = DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles.hab")
    let skyGardenGame = PatchGame(
        id: DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles.hab"), name: "Thistledown Isles",
        fileName: "Thistledown Isles.hab", kind: .file, systemLabel: "Handheld Advance"
    )
    /// The size of Thistledown Isles in the demo library.
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
        // No save sync on connect: it would run in the background, and its
        // requests could take the answers a test queues for the patch flow
        // (`pak.enqueue`), depending on the speed of the machine.
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
        keeper.apps.append(app)
        guard pair else { return app }
        try await app.pairDirectly(with: pak)
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
            case .preparing, .sending, .starting, .cancellingUpload:
                try? await Task.sleep(for: .milliseconds(10))
            case .patching:
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
        #expect(flow.fileName == "Thistledown Isles (patched).hab")
        #expect(!flow.canStart)
        let bps = DemoPatch.bps(sourceSize: skyGardenSize, sourceCRC32: 1, target: Data(count: 64))
        let file = try await picked(app, name: "translation.bps", contents: bps)
        flow.file = file
        flow.fileName = "Thistledown Isles (FR).hab"
        #expect(flow.canStart)
        let revision = session.libraryRevision
        flow.start()
        await runToTheEnd(flow)
        let produced = DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles (FR).hab")
        #expect(flow.step == .succeeded(gameId: produced, late: false))
        // Sent to the staging area, then patched by its stagingId.
        #expect(pak.log.contains { $0.method == "POST" && $0.path.hasSuffix("/uploads") })
        #expect(pak.log.contains { $0.method == "POST" && $0.path.hasSuffix("/patch") && $0.status == 202 })
        // The job is also on the Transfers screen.
        let item = try #require(session.transfers.jobs.first)
        #expect(item.kind == .patch)
        #expect(item.title.contains("Thistledown Isles"))
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
        #expect(flow.mismatchDetails?.expectedSize == 1234)
        #expect(flow.mismatchDetails?.actualSize == Int64(skyGardenSize))
        #expect(AppError.patchMismatch.message != AppError.unsupportedPatch.message)
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
        // Same words in English and French.
        #expect(AppError.unsupportedPatch.message.contains("IPS, BPS"))
    }

    @Test func aPatchOfTheSDCardReplacesTheGame() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let sorted = SdFile.sortedForGame(files, fileName: "Thistledown Isles.hab", name: "Thistledown Isles")
        #expect(sorted.first?.fileName == "Thistledown Isles (Translation).bps")
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = sorted.first?.id
        flow.mode = .replace
        // The name does not matter for a replacement.
        flow.fileName = ""
        #expect(flow.canStart)
        #expect(!flow.closesByItself)
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .succeeded(gameId: skyGarden, late: false))
        #expect(flow.verified == true)
        // Verified: the Patch screen closes by itself.
        #expect(flow.closesByItself)
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
        let identity = try #require(try app.store.credentials(for: pak.deviceId)?.identity)
        pak.restart()
        pak.addClient(identity)
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.jobLost))
    }

    @Test func whatCanBeStarted() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let multiDisc = PatchFlow(session: session, game: PatchGame(
            id: "g", name: "Orrery of Nine Tides", fileName: "Orrery of Nine Tides", kind: .folderM3U, systemLabel: "Disc Console"
        ))
        multiDisc.source = .sdCard
        multiDisc.sdFileId = "sd_1"
        #expect(!multiDisc.isPatchable && !multiDisc.canStart)

        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = "sd_1"
        flow.fileName = "../x.hab"
        #expect(!flow.canStart)
        // Another ending: the ROM's extension is added, as by the pak.
        flow.fileName = "Thistledown Isles (FR).pcr"
        #expect(flow.canStart && flow.outputName == .valid("Thistledown Isles (FR).pcr.hab"))
        // No extension: the ROM's is added.
        flow.fileName = "Thistledown Isles (FR)"
        #expect(flow.canStart && flow.outputName == .valid("Thistledown Isles (FR).hab"))
        flow.fileName = "Thistledown Isles (FR).pcr"
        flow.mode = .replace
        #expect(flow.canStart)
        // An archive ROM is not offered.
        let archive = PatchFlow(session: session, game: PatchGame(
            id: "z", name: "Packed", fileName: "Packed.zip", kind: .file, systemLabel: "Handheld Advance"
        ))
        archive.source = .sdCard
        archive.sdFileId = "sd_1"
        #expect(archive.game?.refusal == .archive && !archive.canStart)
        // No game yet (a shared patch).
        let shared = PatchFlow(session: session, game: nil)
        shared.source = .sdCard
        shared.sdFileId = "sd_1"
        #expect(!shared.canStart)
        shared.game = skyGardenGame
        #expect(shared.fileName == "Thistledown Isles (patched).hab")
        #expect(shared.canStart)
    }

    @Test func aSharedPatchOpensThePatchScreen() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let directory = root.appending(path: "share-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let patch = directory.appending(path: "Hack.bps")
        try Data("BPS1".utf8).write(to: patch)
        let request = try app.shareInbox.write(files: [patch])
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
        let rom = directory.appending(path: "Homebrew.hab")
        try Data(count: 4).write(to: rom)
        try app.shareInbox.write(files: [rom])
        app.checkShareInbox()
        guard case .add = app.sheet else { Issue.record("no add sheet"); return }
    }

    /// The French text asked for by the milestone, whatever the language
    /// of the simulator.
    @Test func mismatchTextInFrench() throws {
        let path = try #require(Bundle.main.path(forResource: "fr", ofType: "lproj"))
        let french = try #require(Bundle(path: path))
        let key = "This patch does not match this ROM. Check the ROM version the patch asks for."
        #expect(french.localizedString(forKey: key, value: nil, table: nil)
            == "Ce patch ne correspond pas à cette ROM. Vérifie la version de la ROM demandée par le patch.")
    }

    /// Runs a job to its end through the API, without the flow.
    func finishOnTheConsole(_ jobId: String, _ session: ConsoleSession) async throws {
        for _ in 0..<10 {
            let job = try await session.perform { try await $0.job(id: jobId) }
            if job.state.isFinished { return }
        }
    }

    @Test func aPatchCancelledTooLateShowsItsResult() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.cancelPollInterval = .milliseconds(1)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        await wait(flow) { $0 == .patching }
        try await finishOnTheConsole(try #require(flow.job?.id), session)
        await flow.cancel()
        #expect(flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles (patched).hab"), late: true))
    }

    @Test func cancellingWhileTheFileIsSentNeverPatches() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let flow = PatchFlow(session: app.session(for: pak.deviceId), game: skyGardenGame)
        flow.file = try await picked(app, name: "translation.bps", contents: DemoPatch.bps(sourceSize: skyGardenSize, sourceCRC32: 1, target: Data(count: 64)))
        // At once (the request is not sent yet), then while sending.
        flow.start()
        await flow.cancel()
        await wait(flow) { !$0.isBusy }
        #expect(flow.step == .cancelled)
        #expect(!flow.isCancelling)
        flow.mode = .replace
        // Each request takes 100 ms: the cancellation lands while sending.
        pak.responseDelay = 0.1
        flow.start()
        await wait(flow) { if case .sending = $0 { return true }; return false }
        await flow.cancel()
        #expect(flow.step == .cancelled)
        try await Task.sleep(for: .milliseconds(800))
        #expect(flow.step == .cancelled)
        #expect(!pak.log.contains { $0.path.hasSuffix("/patch") })
    }

    @Test func jobErrorsOfAPatch() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        pak.romChangesDuringNextPatch = true
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.romChangedDuringPatch))

        // A ROM already patched.
        flow.source = .files
        flow.file = try await picked(app, name: "hack.ups", contents: DemoPatch.upsHeader(sourceSize: 100, targetSize: skyGardenSize))
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .failed(.alreadyPatched))
    }

    @Test func aJobCancelledFromTransfersEndsCancelled() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        await wait(flow) { $0 == .patching }
        let item = try #require(flow.job)
        await flow.tick()
        // The Transfers screen cancels the running patch.
        try await session.transfers.cancel(item)
        #expect(item.job?.cancelRequested == true)
        await runToTheEnd(flow)
        #expect(flow.step == .cancelled)
    }

    @Test func anIPSIsAppliedUnverified() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.fileName = "Bonus"
        flow.start()
        await runToTheEnd(flow)
        #expect(flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (HA)/Bonus.hab"), late: false))
        #expect(flow.verified == false)
        // The warning must be read: the Patch screen stays.
        #expect(!flow.closesByItself)
    }

    @Test func aPollingErrorIsShownAndTheJobFollowedAgain() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        await wait(flow) { $0 == .patching }
        #expect(flow.isBoundToConsole)
        pak.enqueue(.networkFailure(.networkConnectionLost))
        await flow.tick()
        #expect(flow.pollError == .api(.network(.networkConnectionLost)))
        #expect(flow.step == .patching)
        await runToTheEnd(flow)
        #expect(flow.pollError == nil)
        #expect(flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles (patched).hab"), late: false))
    }

    /// "Try Again" while the timer polls too: the patch job is seen
    /// finished twice; the flow moves on once, and nothing is started again.
    @Test func twoPollsOfTheSameJobAdvanceOnce() async throws {
        let pak = makePak()
        let app = try await makeApp(pak)
        let session = app.session(for: pak.deviceId)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        await wait(flow) { $0 == .patching }
        let jobId = try #require(flow.job?.id)
        // Polled on the console until the next poll finishes it.
        var job = try await session.perform { try await $0.job(id: jobId) }
        while job.state == .queued || (job.progress.total ?? 0) - job.progress.done > (job.progress.total ?? 0) / 3 {
            job = try await session.perform { try await $0.job(id: jobId) }
        }
        #expect(job.state == .running)
        pak.responseDelay = 0.2
        async let first: Void = flow.tick()
        async let second: Void = flow.tick()
        _ = await (first, second)
        pak.responseDelay = 0
        #expect(flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles (patched).hab"), late: false))
        #expect(pak.log.filter { $0.path.hasSuffix("/patch") }.count == 1)
    }

    @Test func patchErrorTexts() {
        let mismatch = ServerError(status: 0, code: .patchMismatch, message: "")
        let format = ServerError(status: 422, code: .unsupportedFormat, message: "")
        let full = ServerError(status: 507, code: .insufficientStorage, message: "")
        #expect(AppError.patch(APIError.server(mismatch)) == .patchMismatch)
        #expect(AppError.patch(APIError.server(format)) == .unsupportedPatch)
        #expect(AppError.patch(APIError.server(full)) == .api(.server(full)))
        #expect(AppError.patch(CancellationError()) == .cancelled)
        #expect(AppError.patchJob(mismatch) == .patchMismatch)
        #expect(AppError.patchJob(full) == .api(.server(full)))
        #expect(AppError.jobLost.action == nil && AppError.patchMismatch.action == nil)
        let already = ServerError(status: 0, code: .patchMismatch, message: "", details: ["reason": .string("already-patched")])
        #expect(AppError.patch(APIError.server(already)) == .alreadyPatched)
        #expect(AppError.patchJob(ServerError(status: 0, code: .conflict, message: "")) == .romChangedDuringPatch)
        #expect(AppError.patchJob(ServerError(status: 0, code: .tooLarge, message: "")) == .patchTooLarge)
    }
}
