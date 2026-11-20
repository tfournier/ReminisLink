import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M16: the demo mode of the published app. It pairs with its
/// fake pak without the network, the Keychain or iCloud Drive, leaves the
/// real consoles alone (and pauses their work), and quitting it stops and
/// waits for its work, then leaves no trace and frees it.
@MainActor
@Suite("Demo mode", .serialized)
struct DemoModeAppTests {
    let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-m16-\(UUID().uuidString)")

    /// A demo of its own (the default: a new folder, suite and fake pak
    /// host per session, as in the app), with a short second erasure.
    func makeDemo() -> DemoMode {
        let demo = DemoMode(background: NoBackgroundActivity())
        demo.eraseAgainAfter = .milliseconds(50)
        return demo
    }

    /// The real app of the tests: its own in-memory store and a fake pak
    /// of its own (another host), paired.
    func makeLive() async throws -> (AppModel, FakePak) {
        let pak = FakePak(host: "m16-live-\(UUID().uuidString.lowercased()).test")
        pak.register()
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: "ReminisLinkTests.\(UUID().uuidString)")!)
        let app = AppModel(
            store: store, discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: scratch.appending(path: "art")),
            shareInbox: ShareInbox(root: scratch.appending(path: "inbox")),
            transferDirectory: scratch.appending(path: "transfers"),
            vaultProvider: .folder(scratch.appending(path: "Saves"), location: .local),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        app.discovery.start()
        try await app.pairDirectly(with: pak)
        return (app, pak)
    }

    func waitUntil(timeout: Duration = .seconds(10), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    func waitForSync(_ state: SaveSyncState) async {
        await waitUntil { !state.isRunning && state.phase != .idle }
    }

    @Test func theDemoPairsWithItsFakePakAndOpensItsConsole() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        let demo = makeDemo()
        await root.startDemo { demo }
        #expect(root.demo === demo)
        #expect(root.current === demo.app)
        #expect(demo.app.isDemo)
        #expect(demo.app.path == [.console(demo.pak.deviceId)])
        let device = try #require(demo.app.store.devices.first)
        #expect(demo.app.store.devices.count == 1)
        #expect(device.model == .demo)
        #expect(device.name == String(localized: "Demo Brick"))
        #expect(demo.root.lastPathComponent.hasPrefix(DemoMode.prefix))
        // Signed requests work.
        let session = demo.app.session(for: demo.pak.deviceId)
        await session.connect()
        #expect(session.state == .connected)
        // The real list of consoles is untouched.
        #expect(live.store.devices.count == 1)
        #expect(live.store.devices.first?.model == .brick)
        root.quitDemo()
        await root.waitForEndedDemos()
    }

    @Test func theDemoNeverReachesTheNetwork() async throws {
        let demo = makeDemo()
        try await demo.pair()
        let before = OfflineURLProtocol.failedRequests
        // Any host the fake pak does not serve fails at once, offline,
        // instead of going out (no DNS lookup, no connection).
        let stranger = demo.app.makeClient(endpoint: Endpoint(host: "192.0.2.1", port: 47821), expectedDeviceId: demo.pak.deviceId, pin: demo.pak.pin)
        await #expect(throws: (any Error).self) { try await stranger.hello() }
        #expect(OfflineURLProtocol.failedRequests > before)
        let afterFirst = OfflineURLProtocol.failedRequests
        // Once ended, its own fake pak is gone too: still offline.
        await demo.end()
        let client = demo.app.makeClient(endpoint: demo.pak.endpoint, expectedDeviceId: demo.pak.deviceId, pin: demo.pak.pin)
        await #expect(throws: (any Error).self) { try await client.hello() }
        #expect(OfflineURLProtocol.failedRequests > afterFirst)
    }

    @Test func theDemoKeepsItsKeysInMemoryAndItsSavesOutOfICloud() async throws {
        // The identity and pairing never go to the Keychain of the app.
        let keychains = [
            KeychainStore(service: "ReminisLink.pairing", synchronizable: true),
            KeychainStore(service: "ReminisLink.devices"),
        ]
        let before = try keychains.map { try $0.allAccounts() }
        let demo = makeDemo()
        try await demo.pair()
        let identity = try demo.app.store.identity()
        let after = try keychains.map { try $0.allAccounts() }
        #expect(after == before)
        #expect(!after.joined().contains { $0.contains(identity.clientId) || $0.contains(demo.pak.deviceId) })
        // Synced saves go to the session's temporary folder.
        await demo.app.session(for: demo.pak.deviceId).connect()
        let state = demo.app.saveSync(for: demo.pak.deviceId)
        await waitForSync(state)
        guard case .finished(let report) = state.phase else { Issue.record("no report: \(state.phase)"); return }
        #expect(report.location == .demo)
        #expect(!report.stored.isEmpty)
        #expect(demo.app.vaultLocation == .demo)
        #expect(FileManager.default.fileExists(atPath: demo.root.appending(path: "Saves").path))
        #expect(demo.root.path.hasPrefix(FileManager.default.temporaryDirectory.path))
        await demo.end()
    }

    @Test func theDemoDrawsOriginalArtwork() async throws {
        let demo = makeDemo()
        try await demo.pair()
        #expect(demo.pak.drawsArtwork)
        let cover = DemoArtwork.cover(for: "Thistledown Isles")
        #expect(ArtImage.pngSize(cover)?.width == 512)
        #expect(cover == DemoArtwork.cover(for: "Thistledown Isles"))
        #expect(cover != DemoArtwork.cover(for: "Mosslight Maze"))
        #expect(ArtImage.pngSize(DemoArtwork.screen(for: "Thistledown Isles", slot: 1))?.width == 640)
        await demo.end()
    }

    @Test func quittingDuringASyncLeavesNoTrace() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        let demo = makeDemo()
        // Slow answers: the sync is still running when the user quits.
        demo.pak.responseDelay = 0.05
        await root.startDemo { demo }
        demo.app.syncOnConnect = false
        demo.defaults.set("size", forKey: "gameSort")
        let session = demo.app.session(for: demo.pak.deviceId)
        await session.connect()
        demo.app.startSaveSync(deviceId: demo.pak.deviceId)
        #expect(demo.app.saveSync(for: demo.pak.deviceId).isRunning)
        #expect(FileManager.default.fileExists(atPath: demo.root.path))
        #expect(UserDefaults.standard.persistentDomain(forName: demo.suiteName)?.isEmpty == false)

        root.quitDemo()
        #expect(root.demo == nil)
        #expect(root.current === live)
        await root.waitForEndedDemos()
        #expect(!demo.app.saveSync(for: demo.pak.deviceId).isRunning)
        #expect(!FileManager.default.fileExists(atPath: demo.root.path))
        #expect(UserDefaults.standard.persistentDomain(forName: demo.suiteName)?.isEmpty ?? true)
        #expect(!FileManager.default.fileExists(atPath: DemoMode.preferencesFile(suiteName: demo.suiteName).path))
        #expect(live.store.devices.count == 1)
    }

    @Test func quittingDuringAFileCopyCancelsItThenErases() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        let demo = makeDemo()
        await root.startDemo { demo }
        let directory = demo.app.transferDirectory
        let copy = Task { @MainActor in
            try await demo.app.work.run {
                try await Task.sleep(for: .milliseconds(300))
                try FileManager.default.createDirectory(at: directory.appending(path: "late"), withIntermediateDirectories: true)
                try Data("copy".utf8).write(to: directory.appending(path: "late/file.gba"))
            }
        }
        try await Task.sleep(for: .milliseconds(20))
        root.quitDemo()
        // Cancelled (and waited for) before the erasure.
        await #expect(throws: CancellationError.self) { try await copy.value }
        await root.waitForEndedDemos()
        // Never written after the erasure.
        #expect(!FileManager.default.fileExists(atPath: demo.root.path))
        // New work is refused once the session ends.
        await #expect(throws: CancellationError.self) { try await demo.app.work.run { () } }
    }

    @Test func reopeningAtOnceKeepsTheNewSession() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        let first = makeDemo()
        first.eraseAgainAfter = .milliseconds(300)
        await root.startDemo { first }
        root.quitDemo()
        let second = makeDemo()
        await root.startDemo { second }
        second.defaults.set("size", forKey: "gameSort")
        #expect(second.root != first.root)
        #expect(second.suiteName != first.suiteName)
        #expect(second.pak.host != first.pak.host)
        // The first session's late erasure leaves the second alone.
        try await Task.sleep(for: .milliseconds(500))
        #expect(FileManager.default.fileExists(atPath: second.root.path))
        #expect(second.defaults.string(forKey: "gameSort") == "size")
        #expect(!FileManager.default.fileExists(atPath: first.root.path))
        let session = second.app.session(for: second.pak.deviceId)
        await session.connect()
        #expect(session.state == .connected)
        root.quitDemo()
        await root.waitForEndedDemos()
        #expect(!FileManager.default.fileExists(atPath: second.root.path))
    }

    @Test func anEndedDemoIsFreedEvenDuringAnAppsReload() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        weak var weakApp: AppModel?
        weak var weakPak: FakePak?
        weak var weakSession: ConsoleSession?
        do {
            let demo = makeDemo()
            weakApp = demo.app
            weakPak = demo.pak
            await root.startDemo { demo }
            let session = demo.app.session(for: demo.pak.deviceId)
            weakSession = session
            await session.connect()
            await session.paks.load()
            // An Apps reload (as after an installation) is out when the
            // user quits.
            demo.pak.delayNextAnswer(method: "GET", path: APIClient.apiPrefix + "/paks", by: 0.4)
            session.paks.paksChanged()
            _ = Task { await session.paks.load() }
            try await Task.sleep(for: .milliseconds(50))
            root.quitDemo()
        }
        await root.waitForEndedDemos()
        try await Task.sleep(for: .milliseconds(600))
        #expect(weakSession == nil)
        #expect(weakApp == nil)
        #expect(weakPak == nil)
    }

    @Test func aDemoLeftByACrashIsErasedAtTheNextLaunch() throws {
        let name = DemoMode.prefix + UUID().uuidString.lowercased()
        let folder = DemoMode.root(name: name)
        try FileManager.default.createDirectory(at: folder.appending(path: "Transfers"), withIntermediateDirectories: true)
        let suite = DemoMode.suiteName(name: name)
        UserDefaults(suiteName: suite)!.set(true, forKey: "syncSavesOnConnect")
        UserDefaults(suiteName: suite)!.synchronize()
        DemoMode.removeLeftovers()
        #expect(!FileManager.default.fileExists(atPath: folder.path))
        #expect(UserDefaults.standard.persistentDomain(forName: suite)?.isEmpty ?? true)
    }

    @Test func theRealAppPausesItsSyncAndUploadsDuringTheDemo() async throws {
        let (live, livePak) = try await makeLive()
        livePak.chunkSize = 16 << 10
        livePak.responseDelay = 0.05
        let liveSession = live.session(for: livePak.deviceId)
        live.syncOnConnect = false
        await liveSession.connect()
        // Real work in flight: a save sync and an upload of 40 chunks.
        live.startSaveSync(deviceId: livePak.deviceId)
        let system = try #require(try await liveSession.perform { try await $0.systems() }.first { $0.tag == "GBA" })
        let file = scratch.appending(path: "src/Upload.gba")
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data((0..<(640 << 10)).map { UInt8(truncatingIfNeeded: $0) }).write(to: file)
        let item = try liveSession.transfers.enqueue(
            fileURL: file, target: .rom(systemId: system.id, fileName: "Upload.gba"), destination: "GBA", ownsFile: false
        )
        await waitUntil { item.phase == .uploading }

        let root = AppRoot(live: live)
        let demo = makeDemo()
        await root.startDemo { demo }
        #expect(live.syncsPausedForDemo == [livePak.deviceId])
        #expect(liveSession.transfers.isSuspended)
        // The current chunk ends, then nothing more reaches the real pak.
        await waitUntil { item.phase == .paused }
        #expect(item.phase == .paused)
        #expect(!live.saveSync(for: livePak.deviceId).isRunning)
        // (A request already sent when the demo opened may still land.)
        try await Task.sleep(for: .milliseconds(200))
        let requests = livePak.log.count
        try await Task.sleep(for: .milliseconds(500))
        #expect(livePak.log.count == requests, "\(livePak.log.suffix(from: requests).map { "\($0.method) \($0.path)" })")

        // Quit: the upload goes on and the sync starts again.
        root.quitDemo()
        await waitUntil(timeout: .seconds(20)) { item.phase == .done }
        #expect(item.phase == .done)
        let sync = live.saveSync(for: livePak.deviceId)
        await waitForSync(sync)
        guard case .finished = sync.phase else { Issue.record("no sync after the demo: \(sync.phase)"); return }
        await root.waitForEndedDemos()
    }

    @Test func shuttingDownBeforeASyncFirstRunsKeepsNoSession() async throws {
        weak var weakApp: AppModel?
        do {
            let demo = makeDemo()
            weakApp = demo.app
            try await demo.pair()
            demo.app.syncOnConnect = false
            await demo.app.session(for: demo.pak.deviceId).connect()
            // Asked, then shut down in the same turn: the sync task has
            // not run yet.
            demo.app.startSaveSync(deviceId: demo.pak.deviceId)
            await demo.end()
            #expect(demo.app.sessionCount == 0)
            // Late callers get nothing kept, nothing started.
            _ = demo.app.session(for: demo.pak.deviceId)
            demo.app.startSaveSync(deviceId: demo.pak.deviceId)
            #expect(demo.app.sessionCount == 0)
            #expect(!demo.app.saveSync(for: demo.pak.deviceId).isRunning)
        }
        try await Task.sleep(for: .milliseconds(300))
        #expect(weakApp == nil)
    }

    @Test func quittingCancelsAPatchUploadBeforeItsChunkFolder() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        let demo = makeDemo()
        await root.startDemo { demo }
        let session = demo.app.session(for: demo.pak.deviceId)
        await session.connect()
        let file = demo.app.transferDirectory.appending(path: "src/Fix.bps")
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data(repeating: 1, count: 40_000).write(to: file)
        let scratch = demo.app.transferDirectory.appending(path: "chunks-patch")
        let upload = ResumableUpload(
            source: UploadSource(fileURL: file, sizeBytes: 40_000, sha256: String(repeating: "0", count: 64)),
            target: .staging(fileName: "Fix.bps"), onConflict: .rename, scratchDirectory: scratch
        )
        let client = try await session.connectedClient()
        // As PatchFlow does: the hashing, then the upload, as tracked work.
        let patch = Task { @MainActor in
            try await demo.app.work.run {
                try await Task.sleep(for: .milliseconds(300))
                return try await upload.run(client: client)
            }
        }
        try await Task.sleep(for: .milliseconds(20))
        root.quitDemo()
        await #expect(throws: CancellationError.self) { try await patch.value }
        await root.waitForEndedDemos()
        #expect(!FileManager.default.fileExists(atPath: scratch.path))
        #expect(!FileManager.default.fileExists(atPath: demo.root.path))
    }

    @Test func quittingNeverWaitsForeverForAStuckOperation() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        let demo = makeDemo()
        demo.shutdownTimeout = .milliseconds(300)
        await root.startDemo { demo }
        // A file provider that never answers, whatever the cancellation.
        _ = Task { @MainActor in
            try await demo.app.work.run {
                await withCheckedContinuation { (_: CheckedContinuation<Void, Never>) in }
            }
        }
        try await Task.sleep(for: .milliseconds(20))
        let start = ContinuousClock.now
        root.quitDemo()
        await root.waitForEndedDemos()
        #expect(ContinuousClock.now - start < .seconds(3))
        #expect(demo.endTimedOut)
        #expect(!FileManager.default.fileExists(atPath: demo.root.path))
        #expect(root.demo == nil)
    }

    @Test func theRealAppStartsNothingNewDuringTheDemo() async throws {
        let (live, livePak) = try await makeLive()
        let liveSession = live.session(for: livePak.deviceId)
        live.syncOnConnect = false
        await liveSession.connect()
        let system = try #require(try await liveSession.perform { try await $0.systems() }.first { $0.tag == "GBA" })
        let root = AppRoot(live: live)
        let demo = makeDemo()
        await root.startDemo { demo }
        let requests = livePak.log.count
        // A sync asked during the demo (a connection that ended then) waits;
        // so do a trash read and a new upload.
        live.startSaveSync(deviceId: livePak.deviceId)
        #expect(!live.saveSync(for: livePak.deviceId).isRunning)
        #expect(live.syncsPausedForDemo == [livePak.deviceId])
        #expect(await liveSession.loadTrash() == false)
        let file = scratch.appending(path: "src/Later.gba")
        try FileManager.default.createDirectory(at: file.deletingLastPathComponent(), withIntermediateDirectories: true)
        try Data(repeating: 2, count: 30_000).write(to: file)
        let item = try liveSession.transfers.enqueue(
            fileURL: file, target: .rom(systemId: system.id, fileName: "Later.gba"), destination: "GBA", ownsFile: false
        )
        try await Task.sleep(for: .milliseconds(300))
        #expect(livePak.log.count == requests, "\(livePak.log.suffix(from: requests).map { "\($0.method) \($0.path)" })")
        #expect(item.phase == .waiting)
        // Quit: the upload and the sync asked meanwhile run.
        root.quitDemo()
        await waitUntil { item.phase == .done }
        #expect(item.phase == .done)
        let sync = live.saveSync(for: livePak.deviceId)
        await waitForSync(sync)
        guard case .finished = sync.phase else { Issue.record("no sync after the demo: \(sync.phase)"); return }
        await root.waitForEndedDemos()
    }

    @Test func aLinkOpenedDuringTheDemoGoesToTheRealApp() async throws {
        let (live, _) = try await makeLive()
        let root = AppRoot(live: live)
        let demo = makeDemo()
        await root.startDemo { demo }
        root.handle(url: URL(string: "reminislink://pair?v=1&id=x")!)
        #expect(root.demo == nil)
        // The link was handled by the real app (here: refused).
        #expect(live.linkError != nil)
        await root.waitForEndedDemos()
        #expect(!FileManager.default.fileExists(atPath: demo.root.path))
    }
}
