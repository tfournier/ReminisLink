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

    func makeApp(_ pak: FakePak, discovered: Bool = true, lookup: Duration = .milliseconds(300)) -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        return AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: discovered ? [pak.discoveredDevice] : []),
            artCache: ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: suite)),
            lookupTimeout: lookup,
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
    }

    func link(_ pak: FakePak) -> PairingLink {
        PairingLink(deviceId: pak.deviceId, deviceName: pak.deviceName, secret: pak.secret)
    }

    /// The link as the pak prints it, with the (ignored) `h` and `p` of the
    /// pak's real address.
    func urlWithAddress(_ pak: FakePak) -> URL {
        URL(string: link(pak).url.absoluteString + "&h=\(pak.host)&p=\(pak.port)")!
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
        let flow = app.startPairing(.link(link(pak)))
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

    @Test func cancelDuringSearchSendsNothing() async throws {
        let pak = makePak()
        let app = makeApp(pak, discovered: false, lookup: .seconds(5))
        let flow = app.startPairing(.link(try #require(PairingLink(url: urlWithAddress(pak)))))
        try await Task.sleep(for: .milliseconds(100))
        flow.cancel()
        await flow.wait()
        try await Task.sleep(for: .milliseconds(300))
        #expect(pak.log.isEmpty)
        #expect(app.store.devices.isEmpty)
        #expect(flow.error == nil)
    }

    /// The address of the QR code is never used (docs/specs/03-protocole.md
    /// §3.1): without Bonjour, a new console is not found.
    @Test func newConsoleWithoutBonjourIsNotFound() async throws {
        let pak = makePak()
        let app = makeApp(pak, discovered: false)
        app.handle(url: urlWithAddress(pak))
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.error == .consoleNotFound)
        #expect(flow.error?.action == .retry)
        #expect(pak.log.isEmpty)
        #expect(app.store.devices.isEmpty)
    }

    @Test func newConsoleWithBonjourIgnoresTheLinkAddress() async throws {
        let pak = makePak()
        let app = makeApp(pak)
        app.handle(url: urlWithAddress(pak))
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.step == .done)
        #expect(app.store.device(id: pak.deviceId) != nil)
    }

    // MARK: 2. Re-pairing a paired console

    @Test func linkForAPairedConsoleAsksFirst() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let link = link(pak)
        app.handle(url: link.url)
        #expect(app.pendingRepair?.deviceId == link.deviceId)
        #expect(app.sheet == nil)
        app.pendingRepair = nil
        #expect(app.sheet == nil)
    }

    @Test func confirmedRepairRequiresBonjour() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak, discovered: false)
        let before = try #require(try app.store.credentials(for: pak.deviceId)).key
        let requests = pak.log.count
        app.pendingRepair = .link(try #require(PairingLink(url: urlWithAddress(pak))))
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
        app.pendingRepair = .link(link(pak))
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
        await waitUntil(timeout: .seconds(1)) { session.state == .idle }
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

    // MARK: Round 2

    @Test func cancelDuringPairStillStoresTheNewKey() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let before = try #require(try app.store.credentials(for: pak.deviceId)).key
        pak.pairDelay = 1
        let pairs = pak.log.filter { $0.path == "/api/v1/pair" }.count
        app.pendingRepair = .link(link(pak))
        app.confirmRepair()
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await waitUntil { pak.log.contains { $0.path == "/api/v1/hello" } }
        try await Task.sleep(for: .milliseconds(300)) // /pair is now in flight
        #expect(pak.log.filter { $0.path == "/api/v1/pair" }.count == pairs) // not answered yet
        flow.cancel()
        #expect(app.sheet == nil)
        await flow.wait()
        let after = try #require(try app.store.credentials(for: pak.deviceId)).key
        #expect(after != before)
        let systems = try await app.session(for: pak.deviceId).perform { try await $0.systems() }
        #expect(systems.count == 3)
    }

    @Test func badSignatureOffersRepair() {
        let error = AppError.api(.server(ServerError(status: 401, code: .badSignature, message: "")))
        #expect(error.action == .repair)
    }

    @Test func cancelledCallerDoesNotCancelTheOthers() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        pak.responseDelay = 0.3
        let session = app.session(for: pak.deviceId)
        let a = Task { await session.connect() }
        let b = Task { try await session.perform { try await $0.systems() } }
        try await Task.sleep(for: .milliseconds(50))
        let start = ContinuousClock.now
        a.cancel()
        await a.value
        #expect(ContinuousClock.now - start < .milliseconds(250))
        #expect(try await b.value.count == 3)
        #expect(session.state == .connected)
    }

    @Test func callerArrivingAfterACancelledConnectionStartsANewOne() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        pak.responseDelay = 0.3
        let session = app.session(for: pak.deviceId)
        let a = Task { await session.connect() }
        try await Task.sleep(for: .milliseconds(50))
        a.cancel()
        await a.value
        let b = Task { try await session.perform { try await $0.systems() } }
        #expect(try await b.value.count == 3)
        try await Task.sleep(for: .milliseconds(500)) // the cancelled one has ended
        #expect(session.state == .connected)
    }

    @Test func startPairingStopsThePreviousFlow() async throws {
        let pak = makePak()
        let app = makeApp(pak, discovered: false, lookup: .seconds(5))
        let first = app.startPairing(.link(link(pak)))
        try await Task.sleep(for: .milliseconds(50))
        app.pendingRepair = .link(link(pak))
        let second = app.startPairing(.link(link(pak)))
        let start = ContinuousClock.now
        await first.wait()
        #expect(ContinuousClock.now - start < .seconds(1))
        #expect(first.error == nil)
        #expect(app.pendingRepair == nil)
        guard case .pairing(let shown) = app.sheet else { Issue.record("no pairing sheet"); return }
        #expect(shown === second)
        second.cancel()
    }

    @Test func startIsIdempotent() async throws {
        let pak = makePak()
        pak.responseDelay = 0.2
        let app = makeApp(pak)
        let flow = app.startPairing(.link(link(pak)))
        flow.start()
        flow.start()
        await flow.wait()
        #expect(flow.step == .done)
        #expect(pak.log.filter { $0.path == "/api/v1/pair" }.count == 1)
    }

    @Test func pinForAPairedConsoleAsksFirst() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.requestPairing(.pin(pak.pin, pak.discoveredDevice))
        #expect(app.pendingRepair?.deviceId == pak.deviceId)
        #expect(app.sheet == nil)
    }

    @Test func confirmationWaitsForTheSheetToClose() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.sheet = .addConsole
        app.requestPairing(link(pak))
        #expect(app.sheet == nil)
        #expect(app.pendingRepair == nil)
        app.sheetDismissed()
        #expect(app.pendingRepair?.deviceId == pak.deviceId)
    }

    @Test func keychainReadErrorIsNotTakenForNoKey() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        try secure.set(Data("garbage".utf8), for: "device." + pak.deviceId)
        await #expect(throws: (any Error).self) { try await app.session(for: pak.deviceId).unpair() }
        #expect(app.store.device(id: pak.deviceId) != nil)
    }
}
