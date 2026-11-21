import CoreImage
import CryptoKit
import CoreImage.CIFilterBuiltins
import Foundation
import ImageIO
import Testing
import UniformTypeIdentifiers
@testable import ReminisLinkKit

/// M15 (universal app, iPad app on a Mac): the pairing shared through the
/// iCloud Keychain reaches a device that opened the app before the sync,
/// and the QR code can be read without VisionKit's scanner.
@MainActor
@Suite("Mac pairing")
struct MacPairingTests {
    let deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    let pin = Base64URL.encode(Data(repeating: 3, count: 32))

    func makeDefaults() -> UserDefaults {
        let suite = "ReminisLinkKitTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        return defaults
    }

    /// Copies items of one device's Keychain to another (iCloud Keychain
    /// delivers them one by one, in any order).
    func deliver(_ accounts: [String], from source: InMemorySecureStore, to target: InMemorySecureStore) throws {
        for account in accounts {
            try target.set(try #require(try source.data(for: account)), for: account)
        }
    }

    // MARK: - Identity convergence

    /// The reported case: the Mac opens Add a Console (its name typed:
    /// a local identity and pointer) before the iPhone's items arrive; the
    /// iPhone pairs the console. Its record arrives before its key: the
    /// console is "awaiting identity" (not "forgotten by the console"),
    /// then works with the iPhone's identity once the key arrives.
    @Test func aMacWithALocalIdentityConvergesToTheSharedPairing() throws {
        let iPhoneKeychain = InMemorySecureStore()
        let macKeychain = InMemorySecureStore()
        let iPhone = DeviceStore(secureStore: iPhoneKeychain, defaults: makeDefaults())
        let mac = DeviceStore(secureStore: macKeychain, defaults: makeDefaults())
        try mac.setPairingName("Mac")
        let macIdentity = try mac.identity()

        let shared = try iPhone.identity()
        try iPhone.save(deviceId: deviceId, name: "Brick", model: .brick, clientId: shared.clientId, pin: pin)
        #expect(shared != macIdentity)

        // The record first.
        try deliver([DeviceStore.account(deviceId: deviceId)], from: iPhoneKeychain, to: macKeychain)
        mac.reload()
        #expect(mac.devices.map(\.id) == [deviceId])
        #expect(try mac.pairingState(for: deviceId) == .awaitingIdentity)
        #expect(try mac.credentials(for: deviceId) == nil)
        // Then the key and the iPhone's pointer (the last writer wins).
        try deliver(
            [DeviceStore.account(clientId: shared.clientId), DeviceStore.identityAccount], from: iPhoneKeychain, to: macKeychain
        )
        mac.reload()
        #expect(try mac.pairingState(for: deviceId) == .paired)
        #expect(try mac.credentials(for: deviceId)?.identity == shared)
        #expect(try mac.identity() == shared)
        // The Mac's own identity item is kept (never deleted automatically).
        #expect(try macKeychain.data(for: DeviceStore.account(clientId: macIdentity.clientId)) != nil)
    }

    /// A pointer whose identity item has not arrived is never replaced:
    /// typing a name only changes the name, and the identity used meanwhile
    /// is created once (no new fork on every call). Before M15, the Mac
    /// replaced the shared pointer with a new identity, which the iPhone
    /// then followed before it had its key, and forked again.
    @Test func aPointerWhoseIdentityHasNotArrivedIsNeverReplaced() throws {
        let iPhoneKeychain = InMemorySecureStore()
        let macKeychain = InMemorySecureStore()
        let iPhone = DeviceStore(secureStore: iPhoneKeychain, defaults: makeDefaults())
        let shared = try iPhone.identity()
        try iPhone.setPairingName("Thomas's devices")
        try deliver([DeviceStore.identityAccount], from: iPhoneKeychain, to: macKeychain)

        let mac = DeviceStore(secureStore: macKeychain, defaults: makeDefaults())
        try mac.setPairingName("Mac")
        let pointer = try #require(try macKeychain.data(for: DeviceStore.identityAccount))
        #expect(String(decoding: pointer, as: UTF8.self).contains(shared.clientId))
        #expect(mac.pairingName() == "Mac")
        let local = try mac.identity()
        #expect(local != shared)
        #expect(try mac.identity() == local)
        #expect(try macKeychain.allAccounts().filter { $0.hasPrefix(DeviceStore.identityPrefix) }.count == 1)

        // The shared key arrives: new pairings use it.
        try deliver([DeviceStore.account(clientId: shared.clientId)], from: iPhoneKeychain, to: macKeychain)
        #expect(try mac.identity() == shared)
    }

    /// Without a usable pointer, the identity that owns pairings wins over
    /// one that owns none, then the smallest client id (every device picks
    /// the same); the pointer is then written.
    @Test func withoutAPointerTheIdentityThatOwnsPairingsWins() throws {
        let keychain = InMemorySecureStore()
        let a = DeviceStore(secureStore: keychain, defaults: makeDefaults())
        let first = try a.identity()
        try a.save(deviceId: deviceId, name: "Brick", model: .brick, clientId: first.clientId, pin: pin)
        try keychain.remove(account: DeviceStore.identityAccount)
        // Another identity without pairings, with a smaller client id.
        let other = ClientIdentity(clientId: "00000000-0000-4000-8000-000000000000", privateKey: .init())
        try keychain.set(identityItem(other), for: DeviceStore.account(clientId: other.clientId))

        let b = DeviceStore(secureStore: keychain, defaults: makeDefaults())
        #expect(try b.identity() == first)
        #expect(try b.identity() == first)
        try keychain.remove(account: DeviceStore.account(deviceId: deviceId))
        try keychain.remove(account: DeviceStore.identityAccount)
        #expect(try b.identity() == other)
    }

    func identityItem(_ identity: ClientIdentity) throws -> Data {
        try JSONSerialization.data(withJSONObject: [
            "clientId": identity.clientId,
            "privateKey": Base64URL.encode(identity.privateKey.rawRepresentation),
        ])
    }

    /// Every change of a shared item is signalled (M15: the app sets the
    /// key-value store marker); reads are not.
    @Test func sharedWritesAreSignalled() throws {
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: makeDefaults())
        var changes = 0
        store.onSharedItemsChange = { changes += 1 }
        _ = try store.identity()
        #expect(changes == 2) // identity item, pointer
        _ = try store.identity()
        store.reload()
        _ = store.pairingName()
        #expect(changes == 2)
        try store.save(deviceId: deviceId, name: "Brick", model: .brick, pin: pin)
        try store.rename(deviceId, to: "Mine")
        try store.remove(deviceId)
        #expect(changes == 5)
    }

    // MARK: - Key-value store marker

    @Test func theMarkerCountsChangesAndCarriesNothingElse() {
        let kvs = FakeKeyValueStore()
        let center = NotificationCenter()
        var reads = 0
        let signal = PairingChangeSignal(store: kvs, center: center) { reads += 1 }
        signal.start()
        #expect(kvs.synchronizations == 1)
        signal.noteLocalChange()
        let token = UUID()
        signal.noteLocalChange(token: token)
        let marker = kvs.values[PairingChangeSignal.key] as? [String: Any]
        #expect(marker?["count"] as? Int == 2)
        #expect(marker?["token"] as? String == token.uuidString)
        #expect(Set(marker?.keys.map { $0 } ?? []) == ["count", "token"])
        #expect(Array(kvs.values.keys) == [PairingChangeSignal.key])
        // Local changes do not trigger a read here.
        #expect(reads == 0)

        // A change from another device: the Keychain is read again; a
        // change of another key is not ours; without the list of keys
        // (initial sync, account change), read anyway.
        let name = NSUbiquitousKeyValueStore.didChangeExternallyNotification
        center.post(name: name, object: nil, userInfo: [NSUbiquitousKeyValueStoreChangedKeysKey: [PairingChangeSignal.key]])
        #expect(reads == 1)
        center.post(name: name, object: nil, userInfo: [NSUbiquitousKeyValueStoreChangedKeysKey: ["other"]])
        #expect(reads == 1)
        center.post(name: name, object: nil, userInfo: nil)
        #expect(reads == 2)
        signal.stop()
        center.post(name: name, object: nil, userInfo: nil)
        #expect(reads == 2)
    }

    // MARK: - QR codes without VisionKit

    static let link = "reminislink://pair?v=2&id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&k=__79_Pv6-fj39vX08_Lx8O_u7ezr6uno5-bl5OPi4eA"
    static let rl1Link = "reminislink://pair?id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Brick&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&v=1"

    /// A QR code picture made with Core Image, with a white margin.
    static func qrImage(_ payload: String, scale: CGFloat = 8) throws -> CGImage {
        let filter = CIFilter.qrCodeGenerator()
        filter.message = Data(payload.utf8)
        filter.correctionLevel = "M"
        let code = try #require(filter.outputImage).transformed(by: CGAffineTransform(scaleX: scale, y: scale))
        let margin = 8 * scale
        let background = CIImage(color: .white).cropped(to: code.extent.insetBy(dx: -margin, dy: -margin))
        let image = code.composited(over: background)
        return try #require(CIContext().createCGImage(image, from: image.extent))
    }

    static func png(_ image: CGImage) throws -> Data {
        let data = NSMutableData()
        let destination = try #require(CGImageDestinationCreateWithData(data, UTType.png.identifier as CFString, 1, nil))
        CGImageDestinationAddImage(destination, image, nil)
        #expect(CGImageDestinationFinalize(destination))
        return data as Data
    }

    @Test func aQRCodePictureIsReadWithVision() throws {
        let payloads = try QRCodeReader.payloads(in: try Self.qrImage(Self.link))
        #expect(payloads == [Self.link])
        let search = PairingCodeSearch(payloads: payloads)
        #expect(search == .link(try PairingLink.parse(Self.link)))
    }

    @Test func anImportedPictureFileIsRead() throws {
        let data = try Self.png(try Self.qrImage(Self.link))
        #expect(try QRCodeReader.payloads(inImageData: data) == [Self.link])
        #expect(try QRCodeReader.payloads(inImageData: Data("not a picture".utf8)) == nil)
    }

    @Test func picturesWithoutAUsableLinkAreSortedOut() throws {
        let blank = try #require(CIContext().createCGImage(
            CIImage(color: .white).cropped(to: CGRect(x: 0, y: 0, width: 200, height: 200)),
            from: CGRect(x: 0, y: 0, width: 200, height: 200)
        ))
        #expect(PairingCodeSearch(payloads: try QRCodeReader.payloads(in: blank)) == .noCode)
        let other = try QRCodeReader.payloads(in: try Self.qrImage("https://example.com"))
        #expect(PairingCodeSearch(payloads: other) == .notAPairingCode)
        let old = try QRCodeReader.payloads(in: try Self.qrImage(Self.rl1Link))
        #expect(PairingCodeSearch(payloads: old) == .refused(.outdatedPak))
        // A usable link wins over the others, wherever it is.
        #expect(PairingCodeSearch(payloads: ["https://example.com", Self.rl1Link, Self.link])
            == .link(try PairingLink.parse(Self.link)))
    }

    /// The live scanners' filter: a refused link once per payload, the
    /// usable link once, other codes never.
    @Test func theScanFilterReportsEachThingOnce() throws {
        var filter = PairingScanFilter()
        #expect(filter.evaluate(["https://example.com"]) == nil)
        #expect(filter.evaluate([Self.rl1Link]) == .failure(.outdatedPak))
        #expect(filter.evaluate([Self.rl1Link]) == nil)
        #expect(filter.evaluate([Self.link]) == .success(try PairingLink.parse(Self.link)))
        #expect(filter.evaluate([Self.link]) == nil)
    }
}

final class FakeKeyValueStore: KeyValueStoring {
    var values: [String: Any] = [:]
    var synchronizations = 0

    func object(forKey key: String) -> Any? { values[key] }
    func set(_ value: Any?, forKey key: String) { values[key] = value }
    func synchronize() -> Bool {
        synchronizations += 1
        return true
    }
}
