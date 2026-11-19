import CryptoKit
import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M10 in the app: the read-only state, "Take Over", the lease
/// released in the background, and the disconnection by the console
/// (`POST /clients/me/connect` only on an explicit action).
@MainActor
@Suite("Write access (M10)", .serialized)
struct WriteAccessAppTests {
    let secure = InMemorySecureStore()

    func makePak() -> FakePak {
        let pak = FakePak(host: "m10-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak) async throws -> (AppModel, String) {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: suite)),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        let clientId = try app.store.installationClientId()
        let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "This iPhone")
        let key = try await APIClient(endpoint: pak.endpoint, session: pak.makeSession()).pair(attempt)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: clientId, key: key)
        return (app, clientId)
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    func count(_ pak: FakePak, _ method: String, _ path: String) -> Int {
        pak.log.filter { $0.method == method && $0.path == "/api/v1" + path }.count
    }

    @Test func inTheForegroundTheFreeLeaseIsTaken() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        app.becomeActive()
        let session = app.session(for: pak.deviceId)
        await session.connect()
        await waitUntil { session.lease.access == .writer }
        #expect(session.lease.access == .writer)
        #expect(pak.writerClient == clientId)
        #expect(session.canWrite)

        // To the background, nothing running: released.
        app.enterBackground()
        await waitUntil { pak.writerClient == nil }
        #expect(count(pak, "DELETE", "/writer") == 1)
        session.lease.stop()
    }

    @Test func anotherWriterMakesTheAppReadOnlyUntilItTakesOver() async throws {
        let pak = makePak()
        pak.simulateOtherWriter(name: "Thomas’s iPhone")
        let (app, clientId) = try await pairedApp(pak)
        app.becomeActive()
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.isReadOnly)
        #expect(session.lease.access == .readOnly(holder: "Thomas’s iPhone", tookOver: false))
        // Opening the app never takes the lease from another client.
        try await Task.sleep(for: .milliseconds(100))
        #expect(pak.writerClient != clientId)

        // A write anyway: 423, explained, nothing changed.
        let trash = try #require(session.trash?.entries.first)
        do {
            try await session.deleteTrashEntry(trash.id)
            Issue.record("a read-only write went through")
        } catch let error as AppError {
            #expect(error.isReadOnly)
            #expect(error.message.contains("Thomas’s iPhone"))
            #expect(error.action == nil)
        }
        #expect(session.state == .connected)

        try await session.takeOver()
        #expect(!session.isReadOnly)
        #expect(pak.writerClient == clientId)
        try await session.deleteTrashEntry(trash.id)
        session.lease.stop()
    }

    @Test func a423SeenOnAWriteSwitchesToReadOnly() async throws {
        let pak = makePak()
        let (app, _) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(!session.isReadOnly)
        pak.simulateOtherWriter(name: "Other iPhone")
        _ = try? await session.emptyTrash()
        await waitUntil { session.isReadOnly }
        #expect(session.lease.access == .readOnly(holder: "Other iPhone", tookOver: false))
    }

    @Test func theFormerWriterSeesWhoTookOverAndItsUploadsPause() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (app, clientId) = try await pairedApp(pak)
        app.becomeActive()
        let session = app.session(for: pak.deviceId)
        await session.connect()
        await waitUntil { session.lease.access == .writer }
        // Another iPhone takes over.
        pak.takeOver(by: pak.simulateOtherWriter(name: "Other iPhone"))
        let file = FileManager.default.temporaryDirectory.appending(path: "m10-\(UUID().uuidString).gba")
        try Data(repeating: 7, count: 40_000).write(to: file)
        let system = try #require(try await session.perform { try await $0.systems() }.first)
        let item = try session.transfers.enqueue(
            fileURL: file, target: .rom(systemId: system.id, fileName: "M10.gba"), destination: "GBA", ownsFile: false
        )
        await session.transfers.waitUntilIdle()
        #expect(item.phase == .paused)
        await waitUntil { session.isReadOnly }
        #expect(session.lease.access == .readOnly(holder: "Other iPhone", tookOver: true))

        // Taken back: the upload resumes by itself.
        try await session.takeOver()
        #expect(pak.writerClient == clientId)
        await waitUntil { item.phase == .done }
        #expect(item.phase == .done)
        session.lease.stop()
    }

    @Test func aDisconnectionIsShownAndOnlyAnExplicitActionReconnects() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        pak.disconnect(clientId: clientId)
        await session.refresh()
        #expect(session.isDisconnected)
        let error = try #require(session.error)
        #expect(error.action == .reconnect)
        #expect(error.message == String(localized: "Disconnected by the console."))

        // Automatic reconnections never lift it.
        await session.connect()
        #expect(session.isDisconnected)
        _ = try? await session.perform { try await $0.systems() }
        #expect(count(pak, "POST", "/clients/me/connect") == 0)
        #expect(pak.isDisconnected(clientId: clientId))

        // "Reconnect" does.
        await session.reconnect()
        #expect(session.state == .connected)
        #expect(count(pak, "POST", "/clients/me/connect") == 1)
        #expect(!pak.isDisconnected(clientId: clientId))

        // A later automatic reconnection does not call it again.
        pak.disconnect(clientId: clientId)
        await session.refresh()
        await session.connect()
        #expect(session.isDisconnected)
        #expect(count(pak, "POST", "/clients/me/connect") == 1)
    }

    @Test func openingTheConsoleFromTheListReconnects() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        pak.disconnect(clientId: clientId)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.isDisconnected)
        // Back to the list, then the user opens the console.
        app.path = []
        app.path = [.console(pak.deviceId)]
        await waitUntil { session.state == .connected }
        #expect(session.state == .connected)
        #expect(count(pak, "POST", "/clients/me/connect") == 1)
    }

    @Test func aForgottenIPhoneIsAskedToPairAgain() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        pak.forget(clientId: clientId)
        await session.refresh()
        let error = try #require(session.error)
        #expect(error.action == .repair)
        #expect(error.message == String(localized: "This console no longer recognizes your iPhone."))
        #expect(count(pak, "POST", "/clients/me/connect") == 0)
    }
}
