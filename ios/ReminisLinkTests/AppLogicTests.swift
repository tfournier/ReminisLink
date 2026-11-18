import CryptoKit
import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// App logic against the fake pak: pairing cancellation, re-pairing,
/// connection states. Each test has its own pak host and stores.
@MainActor
@Suite("App logic", .serialized)
struct AppLogicTests {
    let secure = InMemorySecureStore()

    func makePak() -> FakePak {
        let pak = FakePak(host: "app-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func makeApp(_ pak: FakePak, discovered: Bool = true, lookup: Duration = .milliseconds(300),
                 fallbackDelay: Duration = .milliseconds(100)) -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        return AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: discovered ? [pak.discoveredDevice] : []),
            artCache: ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: suite)),
            lookupTimeout: lookup, fallbackDelay: fallbackDelay,
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
    }

    func link(_ pak: FakePak, fallback: Bool) -> PairingLink {
        PairingLink(deviceId: pak.deviceId, deviceName: pak.deviceName, secret: pak.secret,
                    fallbackEndpoint: fallback ? pak.endpoint : nil)
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    func pairedApp(_ pak: FakePak, discovered: Bool = true, lookup: Duration = .milliseconds(300)) async throws -> AppModel {
        let app = makeApp(pak, discovered: discovered, lookup: lookup)
        let clientId = try app.store.installationClientId()
        let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "Test")
        let key = try await APIClient(endpoint: pak.endpoint, session: pak.makeSession()).pair(attempt)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: clientId, key: key)
        return app
    }

    // MARK: 1. Cancel stops a pairing

    @Test func cancelDuringPairingSavesNothing() async throws {
        let pak = makePak()
        pak.responseDelay = 0.5
        let app = makeApp(pak)
        let flow = app.startPairing(.link(link(pak, fallback: false)))
        await waitUntil { flow.step == .pairing }
        flow.cancel()
        await flow.wait()
        try await Task.sleep(for: .seconds(1))
        #expect(app.store.devices.isEmpty)
        #expect(flow.error == nil)
        #expect(flow.paired == nil)
        #expect(pak.clientIds.isEmpty)
        #expect(app.sheet == nil)
    }

    @Test func cancelDuringSearchNeverUsesTheFallback() async throws {
        let pak = makePak()
        let app = makeApp(pak, discovered: false, lookup: .seconds(5), fallbackDelay: .seconds(2))
        let flow = app.startPairing(.link(link(pak, fallback: true)))
        try await Task.sleep(for: .milliseconds(100))
        flow.cancel()
        await flow.wait()
        try await Task.sleep(for: .milliseconds(300))
        #expect(pak.log.isEmpty)
        #expect(app.store.devices.isEmpty)
        #expect(flow.error == nil)
    }

    @Test func newConsoleCanUseTheFallback() async throws {
        let pak = makePak()
        let app = makeApp(pak, discovered: false)
        let flow = app.startPairing(.link(link(pak, fallback: true)))
        await flow.wait()
        #expect(flow.step == .done)
        #expect(app.store.device(id: pak.deviceId) != nil)
    }

    // MARK: 2. Re-pairing a paired console

    @Test func linkForAPairedConsoleAsksFirst() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let link = link(pak, fallback: false)
        app.handle(url: link.url)
        #expect(app.pendingRepair == link)
        #expect(app.sheet == nil)
        app.pendingRepair = nil
        #expect(app.sheet == nil)
    }

    @Test func confirmedRepairRequiresBonjour() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak, discovered: false)
        let before = try #require(try app.store.credentials(for: pak.deviceId)).key
        let requests = pak.log.count
        app.pendingRepair = link(pak, fallback: true)
        app.confirmRepair()
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.error == .consoleNotFound)
        #expect(pak.log.count == requests)
        #expect(try app.store.credentials(for: pak.deviceId)?.key == before)
    }

    @Test func confirmedRepairWithBonjourReplacesTheKey() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let before = try #require(try app.store.credentials(for: pak.deviceId)).key
        app.pendingRepair = link(pak, fallback: false)
        app.confirmRepair()
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.step == .done)
        #expect(try app.store.credentials(for: pak.deviceId)?.key != before)
    }

    // MARK: 4. Cancelled connection

    @Test func cancelledConnectionGoesBackToIdle() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak, discovered: false, lookup: .seconds(5))
        let session = app.session(for: pak.deviceId)
        let task = Task { await session.connect() }
        try await Task.sleep(for: .milliseconds(100))
        #expect(session.state == .connecting)
        task.cancel()
        await task.value
        #expect(session.state == .idle)
        #expect(AppError(CancellationError()) == .cancelled)
        #expect(AppError(CancellationError()).isCancellation)
    }

    // MARK: 5. No key

    @Test func missingKeyOffersRepairAndForgetsLocally() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        try secure.remove(account: "device." + pak.deviceId)
        let requests = pak.log.count
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.state == .failed(.api(.notPaired)))
        #expect(AppError.api(.notPaired).action == .repair)
        try await session.unpair()
        #expect(app.store.devices.isEmpty)
        #expect(pak.log.count == requests)
    }

    // MARK: 6. Shared connection

    @Test func requestsDuringAConnectionWaitForIt() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        async let a = session.perform { try await $0.systems() }
        async let b = session.perform { try await $0.systems() }
        let (first, second) = try await (a, b)
        #expect(first.count == 3)
        #expect(second.count == 3)
        #expect(pak.log.filter { $0.path == "/api/v1/hello" }.count == 1)
    }
}
