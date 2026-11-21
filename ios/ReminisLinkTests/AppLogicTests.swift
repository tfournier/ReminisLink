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

    /// A fake pak on an IPv4 address of its own (never reached: the fake
    /// answers through URLProtocol), so the address of its pairing link is
    /// valid (M13: `h` must be an IPv4 address).
    func makePak(pin: String = FakePak.demoPin) -> FakePak {
        let pak = FakePak(pin: pin, host: Self.randomHost())
        pak.register()
        return pak
    }

    static func randomHost() -> String {
        "10.\(Int.random(in: 1...254)).\(Int.random(in: 1...254)).\(Int.random(in: 1...254))"
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

    /// The link of the QR code (pairing v2): the pak's address and pin.
    func link(_ pak: FakePak) -> PairingLink { pak.pairingLink }

    /// The same link without `h` and `p`.
    func linkWithoutAddress(_ pak: FakePak) -> PairingLink {
        PairingLink(deviceId: pak.deviceId, deviceName: pak.deviceName, secret: pak.secret, endpoint: nil, pin: pak.pin)
    }

    let otherPin = Base64URL.encode(Data(repeating: 0x5A, count: 32))

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    func pairedApp(_ pak: FakePak, discovered: Bool = true, lookup: Duration = .milliseconds(300)) async throws -> AppModel {
        let app = makeApp(pak, discovered: discovered, lookup: lookup)
        try await app.pairDirectly(with: pak)
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
        let flow = app.startPairing(.link(linkWithoutAddress(pak)))
        try await Task.sleep(for: .milliseconds(100))
        flow.cancel()
        await flow.wait()
        try await Task.sleep(for: .milliseconds(300))
        #expect(pak.log.isEmpty)
        #expect(app.store.devices.isEmpty)
        #expect(flow.error == nil)
    }

    /// M13 (docs/specs/03-protocole.md §3.1): without Bonjour, the
    /// address of the QR code is used; the pin of the link authenticates
    /// the console.
    @Test func newConsoleWithoutBonjourUsesTheLinkAddress() async throws {
        let pak = makePak()
        let app = makeApp(pak, discovered: false)
        app.handle(url: link(pak).url)
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.step == .done, "\(String(describing: flow.error))")
        #expect(try app.store.credentials(for: pak.deviceId)?.pin == pak.pin)
        #expect(pak.log.first?.path == "/api/v1/hello")
        // Still no Bonjour: the console opens at the validated address.
        #expect(app.store.lastEndpoint(for: pak.deviceId) == pak.endpoint)
        app.finishPairing(deviceId: pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.state == .connected, "\(String(describing: session.error))")
    }

    /// A paired console that Bonjour does not see (multicast filtered) is
    /// reached at its last validated address; without one, not found.
    @Test func aPairedConsoleFallsBackOnItsLastAddress() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak, discovered: false)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.error == .consoleNotFound)
        app.store.noteEndpoint(pak.endpoint, for: pak.deviceId)
        await session.connect()
        #expect(session.state == .connected)
    }

    /// A link without an address (never printed by the pak) still needs
    /// Bonjour.
    @Test func newConsoleWithoutBonjourNorAddressIsNotFound() async throws {
        let pak = makePak()
        let app = makeApp(pak, discovered: false)
        let flow = app.startPairing(.link(linkWithoutAddress(pak)))
        await flow.wait()
        #expect(flow.error == .consoleNotFound)
        #expect(flow.error?.action == .retry)
        #expect(pak.log.isEmpty)
        #expect(app.store.devices.isEmpty)
    }

    /// Bonjour comes first: an address of the link that answers nothing
    /// is not used when Bonjour finds the console.
    @Test func newConsoleWithBonjourPrefersBonjour() async throws {
        let pak = makePak()
        let app = makeApp(pak)
        let stale = PairingLink(
            deviceId: pak.deviceId, deviceName: pak.deviceName, secret: pak.secret,
            endpoint: Endpoint(host: Self.randomHost(), port: 47821), pin: pak.pin
        )
        app.handle(url: stale.url)
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.step == .done)
        #expect(app.store.device(id: pak.deviceId) != nil)
    }

    /// The pairing v2 request: the identity of the store, platform
    /// `apple`, the name typed for the identity (else the device model),
    /// and `X-RL-Platform` on the requests.
    @Test func pairingSendsTheSharedIdentity() async throws {
        let pak = makePak()
        let app = makeApp(pak)
        try app.store.setPairingName("Thomas's Apple devices")
        let flow = app.startPairing(.link(link(pak)))
        await flow.wait()
        #expect(flow.step == .done)
        let identity = try app.store.identity()
        #expect(pak.clientIds == [identity.clientId])
        #expect(pak.platforms(clientId: identity.clientId).paired == "apple")
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.status?.connectedClients.map(\.clientName) == ["Thomas's Apple devices"])
        #expect(["iphone", "ipad", "mac"].contains(pak.platforms(clientId: identity.clientId).lastSeen ?? ""))
    }

    @Test func clientNameFallsBackOnTheDeviceModel() async throws {
        let pak = makePak()
        let app = makeApp(pak)
        #expect(app.clientName == AppModel.defaultClientName)
        try app.store.setPairingName("   ")
        #expect(app.clientName == AppModel.defaultClientName)
        try app.store.setPairingName("Mes appareils")
        #expect(app.clientName == "Mes appareils")
    }

    /// M13: the link of a pak older than M13 asks to update the pak.
    @Test func anRL1LinkAsksToUpdateThePak() async throws {
        let pak = makePak()
        let app = makeApp(pak)
        let rl1 = "reminislink://pair?v=1&id=\(pak.deviceId)&s=\(Base64URL.encode(pak.secret))&h=192.168.1.42&p=47821"
        app.handle(url: try #require(URL(string: rl1)))
        #expect(app.linkError == .outdatedPak)
        #expect(app.sheet == nil)
        #expect(AppError.outdatedPak.message.contains("Update ReminisLink on the console")
            || AppError.outdatedPak.message.contains("Mets à jour ReminisLink sur la console"))
        app.handle(url: try #require(URL(string: "reminislink://pair?v=2&id=x")))
        #expect(app.linkError == .invalidPairingLink)
    }

    /// M13: a console paired before M13 stays listed, marked; its link is
    /// paired at once (nothing to replace, no confirmation), and the mark
    /// goes away.
    @Test func anRL1ConsoleIsPairedAgainFromItsQRCode() async throws {
        let pak = makePak()
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        let legacy = [PairedDevice(id: pak.deviceId, name: "Old Brick", model: .brick, pairedAt: Date(), needsRepair: true)]
        defaults.set(try JSONEncoder().encode(legacy), forKey: "pairedDevices")
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: defaults, legacySecureStore: InMemorySecureStore()),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: suite)),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        #expect(app.store.device(id: pak.deviceId)?.needsRepair == true)
        // Opening it is refused: it is not paired with this identity.
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.error == .api(.notPaired))
        #expect(session.error?.action == .repair)
        app.handle(url: link(pak).url)
        #expect(app.pendingRepair == nil)
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.step == .done)
        #expect(app.store.device(id: pak.deviceId)?.needsRepair == false)
        #expect(app.store.devices.count == 1)
    }

    /// M13: a console presenting another certificate than the pinned one.
    @Test func anIdentityChangeOffersToPairAgain() {
        let error = AppError.api(.identityChanged)
        #expect(error.action == .repair)
        #expect(error.message.contains("QR code"))
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

    @Test func confirmedRepairWithoutBonjourNorAddressIsNotFound() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak, discovered: false)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, pin: otherPin)
        let requests = pak.log.count
        app.pendingRepair = .link(linkWithoutAddress(pak))
        app.confirmRepair()
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.error == .consoleNotFound)
        #expect(pak.log.count == requests)
        #expect(try app.store.credentials(for: pak.deviceId)?.pin == otherPin)
    }

    @Test func confirmedRepairReplacesThePin() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        // As if the console's certificate had changed (SD card reformatted).
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, pin: otherPin)
        app.pendingRepair = .link(link(pak))
        app.confirmRepair()
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await flow.wait()
        #expect(flow.step == .done)
        #expect(try app.store.credentials(for: pak.deviceId)?.pin == pak.pin)
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

    @Test func missingIdentityOffersRepairAndForgetsLocally() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let clientId = try #require(try app.store.credentials(for: pak.deviceId)).clientId
        try secure.remove(account: "identity." + clientId)
        let requests = pak.log.count
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.state == .failed(.api(.notPaired)))
        #expect(AppError.api(.notPaired).action == .repair)
        try await session.unpair()
        #expect(app.store.devices.isEmpty)
        #expect(pak.log.count == requests)
    }

    // MARK: 5b. Compatibility (M17)

    /// A paired console whose pak became too old for the app (or needs a
    /// newer app) says which one to update, with a link, instead of a
    /// generic error.
    @Test func incompatibleConsoleSaysWhichSideToUpdate() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)

        pak.pakVersion = "0.9.0"
        await session.connect()
        #expect(session.state == .failed(.api(.incompatible(.updatePak))))
        #expect(AppError.api(.incompatible(.updatePak)).action == .updatePak)

        pak.pakVersion = "1.0.0"
        pak.minClientVersion = "99.0.0"
        await session.connect()
        #expect(session.state == .failed(.api(.incompatible(.updateApp))))
        #expect(AppError.api(.incompatible(.updateApp)).action == .updateApp)

        // A development build of the pak is not refused.
        pak.pakVersion = "dev"
        pak.minClientVersion = "1.0.0"
        await session.connect()
        #expect(session.state == .connected)
    }

    @Test func incompatibilityMessagesNameTheSideToUpdate() {
        let pakMessage = AppError.api(.incompatible(.updatePak)).message
        #expect(pakMessage.contains("Update the ReminisLink pak on the console") || pakMessage.contains("Mets à jour le pak ReminisLink sur la console"))
        let appMessage = AppError.api(.incompatible(.updateApp)).message
        #expect(appMessage.contains("Update the ReminisLink app") || appMessage.contains("Mets à jour l’app ReminisLink"))
        #expect(UpdateLinks.installPage.absoluteString.hasPrefix("https://reminis.link/"))
        #expect(UpdateLinks.installPage.absoluteString.hasSuffix("/install/"))
        #expect(UpdateLinks.appStore.host == "apps.apple.com")
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

    @Test func cancelDuringPairStillStoresTheNewPairing() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, pin: otherPin)
        pak.pairDelay = 1
        let pairs = pak.log.filter { $0.path == "/api/v1/pair" }.count
        app.pendingRepair = .link(link(pak))
        app.confirmRepair()
        guard case .pairing(let flow) = app.sheet else { Issue.record("no pairing sheet"); return }
        await waitUntil { pak.log.contains { $0.path == "/api/v1/hello" } }
        try await Task.sleep(for: .milliseconds(300)) // /pair is now in flight
        #expect(pak.log.filter { $0.path == "/api/v1/pair" }.count == pairs + 1) // received
        #expect(flow.paired == nil) // not answered yet
        flow.cancel()
        #expect(app.sheet == nil)
        await flow.wait()
        #expect(try app.store.credentials(for: pak.deviceId)?.pin == pak.pin)
        let systems = try await app.session(for: pak.deviceId).perform { try await $0.systems() }
        #expect(systems.count == 3)
    }

    /// A first exchange whose answer is slow, then a second pairing of the
    /// same console: the second /pair waits for the first pairing to be
    /// stored, so the app ends with the pairing the pak kept.
    @Test func twoPairingsOfTheSameConsoleEndPaired() async throws {
        let pak = makePak()
        let app = makeApp(pak)
        pak.delayNextPairs([1])
        let first = app.startPairing(.link(link(pak)))
        await waitUntil { pak.log.contains { $0.path == "/api/v1/hello" } }
        try await Task.sleep(for: .milliseconds(300)) // the first /pair is in flight
        let second = app.startPairing(.link(link(pak)))
        await second.wait()
        await first.wait()
        #expect(second.step == .done)
        #expect(pak.log.filter { $0.path == "/api/v1/pair" }.count == 2)
        let systems = try await app.session(for: pak.deviceId).perform { try await $0.systems() }
        #expect(systems.count == 3)
    }

    /// Closed while waiting for an earlier exchange: its /pair is never sent.
    @Test func aPairingClosedWhileWaitingSendsNothing() async throws {
        let pak = makePak()
        let app = makeApp(pak)
        pak.delayNextPairs([1])
        let first = app.startPairing(.link(link(pak)))
        await waitUntil { pak.log.contains { $0.path == "/api/v1/hello" } }
        try await Task.sleep(for: .milliseconds(300))
        let second = app.startPairing(.link(link(pak)))
        await waitUntil { pak.log.filter { $0.path == "/api/v1/hello" }.count == 2 }
        try await Task.sleep(for: .milliseconds(100))
        second.cancel()
        await second.wait()
        await first.wait()
        #expect(pak.log.filter { $0.path == "/api/v1/pair" }.count == 1)
        let systems = try await app.session(for: pak.deviceId).perform { try await $0.systems() }
        #expect(systems.count == 3)
    }

    /// "Pair Again" opens the scanner for the console of the current screen.
    @Test func pairAgainOpensTheScannerForTheConsoleOfTheScreen() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.path = [.console(pak.deviceId), .library(pak.deviceId)]
        app.showRepair()
        guard case .addConsole(let repairing) = app.sheet else { Issue.record("no sheet"); return }
        #expect(repairing == pak.deviceId)
    }

    @Test func lookupErrorsOfferTheirActions() {
        #expect(AppError.consoleNotFound.action == .retry)
        #expect(AppError.localNetworkDenied.action == .openSettings)
    }

    /// M8: pairing only opens on demand on the console.
    @Test func closedPairingSaysWhereToOpenIt() async throws {
        let pak = makePak()
        pak.pairingOpen = false
        let app = makeApp(pak)
        let flow = app.startPairing(.link(link(pak)))
        await flow.wait()
        #expect(flow.error == .pairingClosed)
        #expect(!pak.log.contains { $0.path == "/api/v1/pair" })
        let message = AppError.pairingClosed.message
        #expect(message.contains("Pair a New Device") || message.contains("Appairer un nouvel appareil"))
    }

    /// Closed between /hello and /pair: the 401 is explained the same way.
    @Test func pairingClosedDuringTheExchangeSaysWhereToOpenIt() async throws {
        let pak = makePak()
        pak.closePairingAfterNextHello()
        let app = makeApp(pak)
        let flow = app.startPairing(.link(link(pak)))
        await flow.wait()
        #expect(pak.log.contains { $0.path == "/api/v1/pair" && $0.status == 401 })
        #expect(flow.error == .pairingClosed)
        #expect(try app.store.credentials(for: pak.deviceId) == nil)
        // Opened again: Try Again pairs.
        pak.pairingOpen = true
        flow.start()
        await flow.wait()
        #expect(flow.step == .done)
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

    @Test func confirmationWaitsForTheSheetToClose() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.sheet = .addConsole()
        app.requestPairing(link(pak))
        #expect(app.sheet == nil)
        #expect(app.pendingRepair == nil)
        app.sheetDismissed()
        #expect(app.pendingRepair?.deviceId == pak.deviceId)
    }

    @Test func keychainReadErrorIsNotTakenForNoKey() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        try secure.set(Data("garbage".utf8), for: "console." + pak.deviceId)
        await #expect(throws: (any Error).self) { try await app.session(for: pak.deviceId).unpair() }
        #expect(app.store.device(id: pak.deviceId) != nil)
    }
}
