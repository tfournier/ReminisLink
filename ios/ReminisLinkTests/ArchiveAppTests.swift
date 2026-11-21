import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M19 in the app: a game's data exported as an archive and
/// imported into another console, the states synced to the vault with the
/// saves, a shared archive opening the import screen.
@MainActor
@Suite("Game archives and state sync in the app", .serialized)
struct ArchiveAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-m19-\(UUID().uuidString)")
    let skyGarden = DemoLibrary.id("Handheld Advance (HA)/Thistledown Isles.hab")

    func makePak(library: DemoLibrary = .sample) -> FakePak {
        let pak = FakePak(deviceId: "dev-\(UUID().uuidString.prefix(8))", host: "m19-\(UUID().uuidString.lowercased()).test", library: library)
        pak.register()
        return pak
    }

    func app(_ paks: [FakePak]) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let app = AppModel(
            store: DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: paks.map(\.discoveredDevice)),
            artCache: ArtCache(directory: root.appending(path: "art")),
            shareInbox: ShareInbox(root: root.appending(path: "inbox")),
            transferDirectory: root.appending(path: "transfers"),
            exportDirectory: root.appending(path: "exports"),
            vaultProvider: .folder(root.appending(path: "Saves-\(UUID().uuidString)"), location: .local),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        for pak in paks { try await app.pairDirectly(with: pak) }
        return app
    }

    func waitFor(_ flow: ArchiveImportFlow, while busy: (ArchiveImportFlow.Step) -> Bool) async {
        for _ in 0..<300 where busy(flow.step) {
            if flow.step == .importing { await flow.tick() }
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    @Test func aGameMovesFromOneConsoleToAnotherThroughAnArchive() async throws {
        var other = DemoLibrary.sample
        other.systems[0].games.removeAll { $0.path == "Thistledown Isles.hab" }
        let first = makePak()
        let second = makePak(library: other)
        let app = try await app([first, second])

        // Export from the first console, with the ROM.
        let progress = FileDownload(fileId: "archive", expected: nil)
        let exported = try await app.session(for: first.deviceId).downloadGameArchive(gameId: skyGarden, includeROM: true, progress: progress)
        #expect(exported.file.url.lastPathComponent == "Thistledown Isles.reminislink.zip")

        // Import into the second one: a new game is suggested.
        let file = try await PickedFile.copy(exported.file.url, into: app.transferDirectory)
        let flow = ArchiveImportFlow(session: app.session(for: second.deviceId), target: nil)
        flow.start(with: file)
        await waitFor(flow) { $0 != .ready && !$0.isFailed }
        let plan = try #require(flow.plan)
        guard case .newGame? = plan.target else { Issue.record("target \(String(describing: plan.target))"); return }
        #expect(plan.importsAnything && plan.replaced.isEmpty)
        flow.confirm()
        await waitFor(flow) { $0.isBusy }
        guard case .succeeded(let result) = flow.step else { Issue.record("step \(flow.step)"); return }
        #expect(result.gameId == skyGarden && result.imported > 0)
        #expect(app.session(for: second.deviceId).transfers.jobs.first?.kind == .archiveImport)
        let detail = try await app.session(for: second.deviceId).gameDetail(id: skyGarden)
        #expect(detail.hasSave && detail.stateCount > 0)
        // The local copy is gone once the console has it.
        #expect(!FileManager.default.fileExists(atPath: file.url.path))
    }

    @Test func aNewGameWhoseROMIsTakenOffersTheGameThere() async throws {
        let pak = makePak()
        let app = try await app([pak])
        let session = app.session(for: pak.deviceId)
        let exported = try await session.downloadGameArchive(gameId: skyGarden, includeROM: true, progress: FileDownload(fileId: "a", expected: nil))
        let file = try await PickedFile.copy(exported.file.url, into: app.transferDirectory)
        let system = DemoLibrary.sample.systems[0].id
        let flow = ArchiveImportFlow(session: session, target: .newGame(systemId: system, folderId: nil))
        flow.start(with: file)
        await waitFor(flow) { $0 != .ready && !$0.isFailed }
        #expect(flow.step == .failed(.archiveGameExists))
        #expect(flow.existingGameId == skyGarden)
        flow.retarget(.game(gameId: skyGarden))
        await waitFor(flow) { $0 != .ready && !$0.isFailed }
        #expect(flow.plan?.entries.first { $0.role == .save }?.action == .replace)
        #expect(flow.plan?.entries.first { $0.role == .rom }?.reason == .gameExists)
    }

    @Test func aFileThatIsNotAnArchiveSaysSo() async throws {
        let pak = makePak()
        let app = try await app([pak])
        let source = root.appending(path: "x.reminislink.zip")
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        try Data("not a zip".utf8).write(to: source)
        let flow = ArchiveImportFlow(session: app.session(for: pak.deviceId), target: nil)
        flow.start(with: try await PickedFile.copy(source, into: app.transferDirectory))
        await waitFor(flow) { !$0.isFailed && $0 != .ready }
        #expect(flow.step == .failed(.invalidArchive))
        #expect(!AppError.invalidArchive.message.isEmpty && AppError.invalidArchive.action == nil)
    }

    @Test func theSyncVersionsTheStatesWithTheSaves() async throws {
        let pak = makePak()
        let app = try await app([pak])
        app.startSaveSync(deviceId: pak.deviceId)
        let state = app.saveSync(for: pak.deviceId)
        for _ in 0..<300 where state.isRunning || state.phase == .idle {
            try? await Task.sleep(for: .milliseconds(20))
        }
        guard case .finished(let report) = state.phase else { Issue.record("no report: \(state.phase)"); return }
        let states = try #require(report.states)
        // The sync on connection may have copied them already: every state
        // is either a new version or identical to its newest one.
        #expect(states.total > 0 && states.stored.count + states.unchanged == states.total && states.failures.isEmpty)
        let vault = await app.currentVault()
        let versions = try await vault.stateVersions(deviceId: pak.deviceId, gameId: skyGarden)
        #expect(!versions.isEmpty)
        let games = try await vault.games(deviceId: pak.deviceId)
        #expect(games.contains { $0.id == skyGarden && !$0.stateSlots.isEmpty })

        // A state version goes back to its slot through the transfer queue.
        let unlocked = try #require(versions.first { $0.info.slot != 0 } ?? versions.first)
        let item = try await app.session(for: pak.deviceId).restore(unlocked, gameId: skyGarden, gameName: "Thistledown Isles", from: vault)
        #expect(item.target == .state(gameId: skyGarden, slot: unlocked.info.slot))
    }

    @Test func aSharedArchiveOpensTheImportScreen() async throws {
        #expect(ShareRequest(files: ["Sky.reminislink.zip"]).isSingleGameArchive)
        #expect(!ShareRequest(files: ["Sky.zip"]).isSingleGameArchive)
        #expect(!ShareRequest(files: ["Sky.reminislink.zip", "b.gba"]).isSingleGameArchive)
    }
}

extension ArchiveImportFlow.Step {
    var isFailed: Bool {
        if case .failed = self { return true }
        return false
    }
}
