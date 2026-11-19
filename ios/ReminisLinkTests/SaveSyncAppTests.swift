import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M9 in the app: the sync on connection (setting), one sync at
/// a time per console, the iCloud configuration.
@MainActor
@Suite("Save sync in the app", .serialized)
struct SaveSyncAppTests {
    let secure = InMemorySecureStore()
    let root = FileManager.default.temporaryDirectory.appending(path: "rl-m9-\(UUID().uuidString)")

    func makePak() -> FakePak {
        let pak = FakePak(host: "m9-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak, settings: UserDefaults? = nil) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            shareInbox: ShareInbox(root: root.appending(path: "inbox")),
            transferDirectory: root.appending(path: "transfers"),
            exportDirectory: root.appending(path: "exports"),
            settings: settings,
            vaultProvider: .folder(root.appending(path: "Saves"), location: .local),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        let clientId = try app.store.installationClientId()
        let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "Test")
        let key = try await APIClient(endpoint: pak.endpoint, session: pak.makeSession()).pair(attempt)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: clientId, key: key)
        return app
    }

    func waitForSync(_ state: SaveSyncState) async {
        for _ in 0..<200 where state.isRunning || state.phase == .idle {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    func listings(_ pak: FakePak) -> Int {
        pak.log.filter { $0.method == "GET" && $0.path.hasSuffix("/api/v1/saves") }.count
    }

    @Test func connectingSyncsTheSavesByDefault() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        #expect(app.syncOnConnect)
        await app.session(for: pak.deviceId).connect()
        let state = app.saveSync(for: pak.deviceId)
        await waitForSync(state)
        guard case .finished(let report) = state.phase else { Issue.record("no report: \(state.phase)"); return }
        #expect(report.stored.count == 4)
        #expect(report.location == .local)
        #expect(app.vaultLocation == .local)
        let games = try await app.currentVault().games(deviceId: pak.deviceId)
        #expect(games.count == 4)
    }

    @Test func theSettingTurnsTheSyncOnConnectionOffAndIsKept() async throws {
        let defaults = UserDefaults(suiteName: "ReminisLinkTests.settings.\(UUID().uuidString)")!
        let pak = makePak()
        let app = try await pairedApp(pak, settings: defaults)
        app.syncOnConnect = false
        await app.session(for: pak.deviceId).connect()
        try await Task.sleep(for: .milliseconds(200))
        #expect(listings(pak) == 0)
        #expect(app.saveSync(for: pak.deviceId).phase == .idle)
        let again = try await pairedApp(pak, settings: defaults)
        #expect(!again.syncOnConnect)
    }

    @Test func aSecondSyncRequestWhileOneRunsDoesNothing() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.syncOnConnect = false
        await app.session(for: pak.deviceId).connect()
        pak.responseDelay = 0.1
        app.startSaveSync(deviceId: pak.deviceId)
        app.startSaveSync(deviceId: pak.deviceId)
        // Also from a new connection of the same console.
        app.didReplaceKey(for: pak.deviceId)
        app.startSaveSync(deviceId: pak.deviceId)
        await waitForSync(app.saveSync(for: pak.deviceId))
        #expect(listings(pak) == 1)
        pak.responseDelay = 0
        app.startSaveSync(deviceId: pak.deviceId)
        await waitForSync(app.saveSync(for: pak.deviceId))
        try await Task.sleep(for: .milliseconds(50))
        #expect(listings(pak) == 2)
    }

    @Test func aCancelledSyncKeepsTheLastReport() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.syncOnConnect = false
        await app.session(for: pak.deviceId).connect()
        let state = app.saveSync(for: pak.deviceId)
        app.startSaveSync(deviceId: pak.deviceId)
        await waitForSync(state)
        let first = state.lastReport
        #expect(first != nil)
        pak.responseDelay = 0.2
        app.startSaveSync(deviceId: pak.deviceId)
        state.cancel()
        for _ in 0..<100 where state.isRunning { try await Task.sleep(for: .milliseconds(20)) }
        #expect(state.phase == .finished(try #require(first)))
        pak.responseDelay = 0
    }

    @Test func syncErrorsHaveTheirOwnTexts() {
        #expect(AppError.sync(SaveSyncError.checksumMismatch(file: "a.sav")) == .syncChecksumMismatch)
        #expect(AppError.sync(SaveVaultError.versionNotFound) == .vault)
        #expect(AppError.sync(APIError.network(.timedOut)) == .api(.network(.timedOut)))
        #expect(AppError.syncChecksumMismatch.message != AppError.unexpected.message)
    }

    /// Build settings are not expanded in Info.plist keys: the folder in
    /// iCloud Drive is declared for the container the app asks for.
    @Test func theICloudContainerIsDeclaredForFiles() throws {
        let container = try #require(Bundle.main.object(forInfoDictionaryKey: "RLICloudContainer") as? String)
        #expect(container.hasPrefix("iCloud."))
        let containers = try #require(Bundle.main.object(forInfoDictionaryKey: "NSUbiquitousContainers") as? [String: Any])
        let entry = try #require(containers[container] as? [String: Any])
        #expect(entry["NSUbiquitousContainerName"] as? String == "ReminisLink")
        #expect(entry["NSUbiquitousContainerIsDocumentScopePublic"] as? Bool == true)
    }
}
