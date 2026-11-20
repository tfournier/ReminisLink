import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M16: the demo mode of the published app. It pairs with its
/// fake pak without the network, the Keychain or iCloud Drive, leaves the
/// real consoles alone, and quitting it leaves no trace.
@MainActor
@Suite("Demo mode", .serialized)
struct DemoModeAppTests {
    /// A demo of its own (folder, defaults suite, fake pak host), so tests
    /// never meet the app's demo.
    func makeDemo() -> DemoMode {
        let id = UUID().uuidString.lowercased()
        let demo = DemoMode(name: "ReminisLinkDemoTest-\(id)", host: "demo-\(id).invalid", background: NoBackgroundActivity())
        // Kept alive past the test, as the app keeps an ended demo: a task
        // of its sessions may still be finishing.
        Self.keptAlive.append(demo)
        return demo
    }

    static var keptAlive: [AnyObject] = []

    func makeRoot(_ live: AppModel) -> AppRoot {
        let root = AppRoot(live: live)
        Self.keptAlive.append(root)
        return root
    }

    /// The real app of the tests: its own in-memory store, one console.
    func makeLive() throws -> AppModel {
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: UserDefaults(suiteName: "ReminisLinkTests.\(UUID().uuidString)")!)
        try store.save(deviceId: "real-console", name: "My Brick", model: .brick, pin: FakePak.demoPin)
        return AppModel(
            store: store, discovery: Discovery(fixedDevices: []), artCache: ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: "rl-m16-\(UUID().uuidString)")),
            makeSession: { FakePak.makeOfflineSession() }
        )
    }

    func waitForSync(_ state: SaveSyncState) async {
        for _ in 0..<200 where state.isRunning || state.phase == .idle {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    @Test func theDemoPairsWithItsFakePakAndOpensItsConsole() async throws {
        let live = try makeLive()
        let root = makeRoot(live)
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
        // Signed requests work: the library of the fake pak.
        let session = demo.app.session(for: demo.pak.deviceId)
        await session.connect()
        #expect(session.state == .connected)
        // The real list of consoles is untouched.
        #expect(live.store.devices.map(\.id) == ["real-console"])
        root.quitDemo()
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
        // Once quit, the fake pak is gone too: still offline.
        demo.tearDown()
        let client = demo.app.makeClient(endpoint: demo.pak.endpoint, expectedDeviceId: demo.pak.deviceId, pin: demo.pak.pin)
        await #expect(throws: (any Error).self) { try await client.hello() }
        #expect(OfflineURLProtocol.failedRequests > afterFirst)
        // No Bonjour browser: the discovery is fixed.
        #expect(demo.app.discovery.devices.isEmpty)
    }

    @Test func theDemoKeepsItsKeysInMemoryAndItsSavesOutOfICloud() async throws {
        // The identity and pairing never go to the Keychain of the app.
        let keychains = [
            KeychainStore(service: "ReminisLink.pairing", synchronizable: true),
            KeychainStore(service: "ReminisLink.devices"),
        ]
        let before = keychains.map { (try? $0.allAccounts()) ?? [] }
        let demo = makeDemo()
        try await demo.pair()
        let identity = try demo.app.store.identity()
        let after = keychains.map { (try? $0.allAccounts()) ?? [] }
        #expect(after == before)
        #expect(!after.joined().contains { $0.contains(identity.clientId) || $0.contains(demo.pak.deviceId) })
        // Synced saves go to the demo's temporary folder.
        await demo.app.session(for: demo.pak.deviceId).connect()
        let state = demo.app.saveSync(for: demo.pak.deviceId)
        await waitForSync(state)
        guard case .finished(let report) = state.phase else { Issue.record("no report: \(state.phase)"); return }
        #expect(report.location == .demo)
        #expect(!report.stored.isEmpty)
        #expect(demo.app.vaultLocation == .demo)
        let saves = demo.root.appending(path: "Saves")
        #expect(FileManager.default.fileExists(atPath: saves.path))
        #expect(demo.root.path.hasPrefix(FileManager.default.temporaryDirectory.path))
        demo.tearDown()
    }

    @Test func quittingLeavesNoTrace() async throws {
        let live = try makeLive()
        let root = makeRoot(live)
        let demo = makeDemo()
        await root.startDemo { demo }
        // Use it: a save sync (files), a setting and the Library sort
        // (defaults), the art cache.
        await demo.app.session(for: demo.pak.deviceId).connect()
        await waitForSync(demo.app.saveSync(for: demo.pak.deviceId))
        demo.app.syncOnConnect = false
        demo.defaults.set("size", forKey: "gameSort")
        demo.defaults.synchronize()
        #expect(FileManager.default.fileExists(atPath: demo.root.path))
        #expect(UserDefaults.standard.persistentDomain(forName: demo.suiteName)?.isEmpty == false)

        root.quitDemo()
        #expect(root.demo == nil)
        #expect(root.current === live)
        #expect(!FileManager.default.fileExists(atPath: demo.root.path))
        #expect(UserDefaults.standard.persistentDomain(forName: demo.suiteName)?.isEmpty ?? true)
        #expect(!FileManager.default.fileExists(atPath: DemoMode.preferencesFile(suiteName: demo.suiteName).path))
        // The real app is as before.
        #expect(live.store.devices.map(\.id) == ["real-console"])
        #expect(live.sheet == nil)
        // A second demo starts afresh.
        let again = makeDemo()
        await root.startDemo { again }
        #expect(again.app.saveSync(for: again.pak.deviceId).lastReport == nil)
        root.quitDemo()
    }

    @Test func aLinkOpenedDuringTheDemoGoesToTheRealApp() async throws {
        let live = try makeLive()
        let root = makeRoot(live)
        let demo = makeDemo()
        await root.startDemo { demo }
        root.handle(url: URL(string: "reminislink://pair?v=1&id=x")!)
        #expect(root.demo == nil)
        #expect(!FileManager.default.fileExists(atPath: demo.root.path))
        // The link was handled by the real app (here: refused).
        #expect(live.linkError != nil)
    }
}
