import CoreImage
import CoreImage.CIFilterBuiltins
import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
import UIKit
@testable import ReminisLink

/// M15, the universal app on a Mac: a console paired on the iPhone reaches
/// the Mac through the iCloud Keychain while the Mac app runs. Nothing in
/// the running app keeps an older (local, or absent) identity once the
/// shared one arrives; a console forgetting the identity is said only after
/// the Keychain was read again.
@MainActor
@Suite("Mac pairing (M15)", .serialized)
struct MacPairingAppTests {
    let iPhoneKeychain = InMemorySecureStore()
    let macKeychain = InMemorySecureStore()

    func makePak() -> FakePak {
        let pak = FakePak(host: "m15-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func makeApp(_ pak: FakePak, keychain: InMemorySecureStore) -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        return AppModel(
            store: DeviceStore(secureStore: keychain, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: suite)),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
    }

    /// iCloud Keychain delivering some items of the iPhone to the Mac.
    func deliver(_ accounts: [String]) throws {
        for account in accounts {
            try macKeychain.set(try #require(try iPhoneKeychain.data(for: account)), for: account)
        }
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    /// The reported case: the Mac app runs with a local identity (Add a
    /// Console opened, name typed) and opens the console as soon as its
    /// record arrives, before the key: "not arrived yet", never "Pair
    /// Again". The key arrives, the list is read again: the session
    /// connects by itself and signs with the shared identity; the Mac's
    /// local identity never reaches the console.
    @Test func aConsoleOpenedBeforeItsKeyArrivedConnectsOnceItDoes() async throws {
        let pak = makePak()
        let mac = makeApp(pak, keychain: macKeychain)
        try mac.store.setPairingName("Mac")
        let local = try mac.store.identity()

        let iPhone = makeApp(pak, keychain: iPhoneKeychain)
        try await iPhone.pairDirectly(with: pak)
        let shared = try iPhone.store.identity()

        try deliver([Account.console(pak.deviceId)])
        mac.reloadPairings()
        #expect(mac.store.devices.map(\.id) == [pak.deviceId])
        let session = mac.session(for: pak.deviceId)
        await session.connect()
        #expect(session.error == .pairingNotSynced)
        #expect(session.error?.action == .retry)
        #expect(pak.platforms(clientId: local.clientId).lastSeen == nil)

        try deliver([Account.identity(shared.clientId), Account.pointer])
        mac.reloadPairings()
        await waitUntil { session.state == .connected }
        #expect(session.state == .connected)
        #expect(pak.platforms(clientId: shared.clientId).lastSeen != nil)
        #expect(pak.platforms(clientId: local.clientId).lastSeen == nil)
        #expect(try mac.store.identity() == shared)
    }

    /// Opening the console again after such a failure reads the Keychain
    /// again (the session was kept, with its failure, before M15).
    @Test func openingTheConsoleAgainReadsTheKeychainAgain() async throws {
        let pak = makePak()
        let mac = makeApp(pak, keychain: macKeychain)
        let iPhone = makeApp(pak, keychain: iPhoneKeychain)
        try await iPhone.pairDirectly(with: pak)
        let shared = try iPhone.store.identity()
        try deliver([Account.console(pak.deviceId)])
        mac.store.reload()
        let session = mac.session(for: pak.deviceId)
        await session.connect()
        #expect(session.error == .pairingNotSynced)

        // The key arrives; no reload of the app: the user opens the console.
        try deliver([Account.identity(shared.clientId)])
        session.userOpened()
        await waitUntil { session.state == .connected }
        #expect(session.state == .connected)
    }

    /// A running session signs with the identity it connected with. The
    /// iPhone pairs the console again with another identity and the console
    /// forgets the old one: the Mac's next read of the Keychain replaces
    /// its client, and an `unknown_client` is never shown for a pairing the
    /// Keychain can renew.
    @Test func aPairingRenewedElsewhereReplacesTheRunningClient() async throws {
        let pak = makePak()
        let mac = makeApp(pak, keychain: macKeychain)
        let iPhone = makeApp(pak, keychain: iPhoneKeychain)
        try await iPhone.pairDirectly(with: pak)
        let old = try iPhone.store.identity()
        try deliver(try iPhoneKeychain.allAccounts())
        mac.reloadPairings()
        let session = mac.session(for: pak.deviceId)
        await session.connect()
        #expect(session.state == .connected)

        // The iPhone pairs again with a new identity (its pointer moved),
        // the console forgets the old one.
        try iPhoneKeychain.remove(account: Account.pointer)
        try iPhoneKeychain.remove(account: Account.identity(old.clientId))
        let renewed = ClientIdentity.generate()
        let attempt = try PairingAttempt(secret: pak.secret, deviceId: pak.deviceId, identity: renewed, clientName: "Test")
        try await APIClient(endpoint: pak.endpoint, pin: pak.pin, session: pak.makeSession()).pair(attempt)
        try iPhoneKeychain.set(
            try JSONSerialization.data(withJSONObject: [
                "clientId": renewed.clientId, "privateKey": Base64URL.encode(renewed.privateKey.rawRepresentation),
            ]),
            for: Account.identity(renewed.clientId)
        )
        try iPhone.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: renewed.clientId, pin: pak.pin)
        pak.forget(clientId: old.clientId)

        // The record and the key reach the Mac; the periodic read (or the
        // key-value store marker) replaces the client.
        try deliver([Account.console(pak.deviceId), Account.identity(renewed.clientId)])
        mac.reloadPairings()
        await waitUntil { session.state == .connected && pak.platforms(clientId: renewed.clientId).lastSeen != nil }
        #expect(pak.platforms(clientId: renewed.clientId).lastSeen != nil)
        #expect(session.state == .connected)
        await session.refresh()
        #expect(session.state == .connected)
    }

    /// A console that really forgot the shared identity (no newer
    /// pairing in the Keychain): "Pair Again" is shown, after one more
    /// read of the Keychain.
    @Test func aConsoleThatForgotTheSharedIdentityAsksToPairAgain() async throws {
        let pak = makePak()
        let mac = makeApp(pak, keychain: macKeychain)
        let iPhone = makeApp(pak, keychain: iPhoneKeychain)
        try await iPhone.pairDirectly(with: pak)
        let old = try iPhone.store.identity()
        try deliver(try iPhoneKeychain.allAccounts())
        mac.reloadPairings()
        pak.forget(clientId: old.clientId)
        let session = mac.session(for: pak.deviceId)
        await session.connect()
        guard case .api(.server(let server))? = session.error else {
            Issue.record("expected unknown_client, got \(String(describing: session.error))")
            return
        }
        #expect(server.code == .unknownClient)
        #expect(session.error?.action == .repair)
        #expect(session.error?.isPairingProblem == true)
        // Nothing changed in the Keychain: the next read keeps the failure.
        mac.reloadPairings()
        #expect(session.error?.action == .repair)
    }

    /// The default name on the consoles: "Mac" for the iPad app on a Mac
    /// (it reports the model "iPad"), the model elsewhere.
    @Test func theDefaultNameOnAMacIsMac() {
        #expect(AppModel.defaultClientName(isiOSAppOnMac: true, model: "iPad") == "Mac")
        #expect(AppModel.defaultClientName(isiOSAppOnMac: false, model: "iPad") == "iPad")
        #expect(AppModel.defaultClientName(isiOSAppOnMac: false, model: "iPhone") == "iPhone")
    }

    /// The scanner: VisionKit where it works, else the camera read with
    /// Vision (the Mac), else no camera (import a picture).
    @Test func theScannerFallsBackWhenVisionKitIsNotSupported() {
        #expect(ScannerKind.choose(dataScannerSupported: true, dataScannerAvailable: true, hasCamera: true) == .visionKit)
        #expect(ScannerKind.choose(dataScannerSupported: false, dataScannerAvailable: false, hasCamera: true) == .camera)
        // Supported but not available (camera access refused): the camera
        // path, which says so.
        #expect(ScannerKind.choose(dataScannerSupported: true, dataScannerAvailable: false, hasCamera: true) == .camera)
        #expect(ScannerKind.choose(dataScannerSupported: false, dataScannerAvailable: false, hasCamera: false) == .none)
        #expect(ScannerKind.current(environment: ["RL_UITEST_SCANNER": "camera"]) == .camera)
        #expect(ScannerKind.current(environment: ["RL_UITEST_SCANNER": "none"]) == .none)
    }

    /// A picture of the QR code goes through the same check as a scan.
    /// Not in the simulator, whose Vision cannot detect barcodes ("Could
    /// not create inference context"); ReminisLinkKit tests the decoding
    /// on macOS.
    #if targetEnvironment(simulator)
    nonisolated static let visionWorks = false
    #else
    nonisolated static let visionWorks = true
    #endif

    @Test(.enabled(if: visionWorks)) func anImportedPictureLeadsToThePairingLink() throws {
        let pak = makePak()
        let data = try QRTestImage.png(pak.pairingLink.url.absoluteString)
        let payloads = try #require(try QRCodeReader.payloads(inImageData: data))
        #expect(PairingCodeSearch(payloads: payloads) == .link(pak.pairingLink))
    }

    /// Unusable pictures are said, never paired.
    @Test func pictureErrorsAreSaid() {
        #expect(AppError.noPairingCodeInPicture.action == nil)
        #expect(!AppError.noPairingCodeInPicture.message.isEmpty)
        #expect(AppError.pairingNotSynced.isPairingProblem)
        #expect(!AppError.pairingNotSynced.message.contains("no longer"))
    }
}

/// QR code pictures made with Core Image.
enum QRTestImage {
    static func png(_ payload: String) throws -> Data {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(payload.utf8)
        let code = try #require(filter.outputImage).transformed(by: CGAffineTransform(scaleX: 8, y: 8))
        let image = code.composited(over: CIImage(color: .white).cropped(to: code.extent.insetBy(dx: -64, dy: -64)))
        let cgImage = try #require(CIContext().createCGImage(image, from: image.extent))
        return try #require(UIImage(cgImage: cgImage).pngData())
    }
}

/// The Keychain accounts of ``DeviceStore`` (docs/specs/06-app-ios.md §6).
enum Account {
    static let pointer = "identity"
    static func identity(_ clientId: String) -> String { "identity." + clientId }
    static func console(_ deviceId: String) -> String { "console." + deviceId }
}
