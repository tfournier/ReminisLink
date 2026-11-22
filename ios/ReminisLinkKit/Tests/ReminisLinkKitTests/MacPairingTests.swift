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
        // Opened before the sync: a local identity and pointer (no pointer
        // had arrived).
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

    /// A pointer whose identity item has not arrived is never replaced,
    /// and nothing is paired with another identity meanwhile: typing a name
    /// only writes the name item; ``DeviceStore/identity()`` says the
    /// identity is arriving. Before M15, the Mac replaced the shared pointer
    /// with a new identity, which the iPhone then followed before it had
    /// its key, and forked again.
    @Test func aPointerWhoseIdentityHasNotArrivedIsNeverReplaced() throws {
        let iPhoneKeychain = InMemorySecureStore()
        let macKeychain = InMemorySecureStore()
        let iPhone = DeviceStore(secureStore: iPhoneKeychain, defaults: makeDefaults())
        let shared = try iPhone.identity()
        try iPhone.setPairingName("Thomas's devices")
        try deliver([DeviceStore.identityAccount, DeviceStore.nameAccount], from: iPhoneKeychain, to: macKeychain)
        let pointer = try #require(try macKeychain.data(for: DeviceStore.identityAccount))

        let mac = DeviceStore(secureStore: macKeychain, defaults: makeDefaults())
        #expect(mac.pairingName() == "Thomas's devices")
        try mac.setPairingName("Mac")
        #expect(mac.pairingName() == "Mac")
        #expect(try macKeychain.data(for: DeviceStore.identityAccount) == pointer)
        #expect(throws: DeviceStoreError.identityNotArrived) { try mac.identity() }
        #expect(throws: DeviceStoreError.identityNotArrived) { try mac.identity() }
        #expect(try macKeychain.data(for: DeviceStore.identityAccount) == pointer)
        #expect(try macKeychain.allAccounts().filter { $0.hasPrefix(DeviceStore.identityPrefix) }.isEmpty)

        // The shared key arrives: new pairings use it.
        try deliver([DeviceStore.account(clientId: shared.clientId)], from: iPhoneKeychain, to: macKeychain)
        #expect(try mac.identity() == shared)
    }

    /// The iPhone's pointer arrives between the Mac's read (no pointer) and
    /// its write: the pointer is created with an add, never an update, so
    /// the iPhone's wins; the Mac waits for its key, then uses it.
    @Test func aPointerArrivingBetweenTheReadAndTheWriteWins() throws {
        let iPhoneKeychain = InMemorySecureStore()
        let iPhone = DeviceStore(secureStore: iPhoneKeychain, defaults: makeDefaults())
        let shared = try iPhone.identity()
        let iPhonePointer = try #require(try iPhoneKeychain.data(for: DeviceStore.identityAccount))
        let macKeychain = ArrivingKeychain()
        macKeychain.beforeAdd = { account in
            if account == DeviceStore.identityAccount { try? macKeychain.base.set(iPhonePointer, for: account) }
        }
        let mac = DeviceStore(secureStore: macKeychain, defaults: makeDefaults())
        #expect(throws: DeviceStoreError.identityNotArrived) { try mac.identity() }
        #expect(try macKeychain.data(for: DeviceStore.identityAccount) == iPhonePointer)
        try macKeychain.set(
            try #require(try iPhoneKeychain.data(for: DeviceStore.account(clientId: shared.clientId))),
            for: DeviceStore.account(clientId: shared.clientId)
        )
        #expect(try mac.identity() == shared)
    }

    /// The name has its own item: renaming never rewrites the pointer; a
    /// pointer written before M15 still gives its name until one is typed.
    @Test func theNameIsKeptApartFromThePointer() throws {
        let keychain = InMemorySecureStore()
        let store = DeviceStore(secureStore: keychain, defaults: makeDefaults())
        let identity = try store.identity()
        try keychain.set(
            try JSONSerialization.data(withJSONObject: ["clientId": identity.clientId, "name": "Old name"]),
            for: DeviceStore.identityAccount
        )
        #expect(store.pairingName() == "Old name")
        let pointer = try keychain.data(for: DeviceStore.identityAccount)
        try store.setPairingName("New name")
        #expect(store.pairingName() == "New name")
        #expect(try keychain.data(for: DeviceStore.identityAccount) == pointer)
        try store.setPairingName("  ")
        #expect(store.pairingName() == nil)
        #expect(try store.identity() == identity)
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
        try store.setPairingName("Mine")
        #expect(changes == 3)
        try store.save(deviceId: deviceId, name: "Brick", model: .brick, pin: pin)
        try store.rename(deviceId, to: "Mine")
        #expect(changes == 5)
        // What a connection notes is not a pairing change: not signalled
        // (two devices reaching the console at different addresses would
        // wake each other up).
        store.noteEndpoint(Endpoint(host: "192.168.1.50", port: 47821), for: deviceId, pin: pin)
        store.noteConsoleName("Renamed on the console", for: deviceId)
        #expect(changes == 5)
        try store.remove(deviceId)
        #expect(changes == 6)
    }

    /// A pointer whose key exists nowhere: "arriving" for 10 minutes
    /// (noted on this device), then "missing"; resetting this device's
    /// identity replaces the pointer (the only path that does), keeps
    /// every identity item, and new pairings work again.
    @Test func anOrphanedPointerCanBeReset() throws {
        let keychain = InMemorySecureStore()
        let lost = ClientIdentity.generate()
        try keychain.set(try JSONSerialization.data(withJSONObject: ["clientId": lost.clientId]), for: DeviceStore.identityAccount)
        let clock = IdentityTestClock()
        let defaults = makeDefaults()
        let store = DeviceStore(secureStore: keychain, defaults: defaults, now: { clock.now })
        let local = ClientIdentity.generate()
        try keychain.set(identityItem(local), for: DeviceStore.account(clientId: local.clientId))
        #expect(throws: DeviceStoreError.identityNotArrived) { try store.identity() }
        clock.now += 9 * 60
        #expect(throws: DeviceStoreError.identityNotArrived) { try store.identity() }
        clock.now += 2 * 60
        #expect(throws: DeviceStoreError.identityMissing) { try store.identity() }
        // Another store on this device (a relaunch) remembers since when.
        #expect(throws: DeviceStoreError.identityMissing) {
            try DeviceStore(secureStore: keychain, defaults: defaults, now: { clock.now }).identity()
        }

        #expect(store.lostIdentityPointer() == lost.clientId)
        var changes = 0
        store.onSharedItemsChange = { changes += 1 }
        // Asked for another pointer (it changed meanwhile): nothing changes.
        #expect(throws: DeviceStoreError.resetNotNeeded) { try store.resetIdentity(replacing: local.clientId) }
        #expect(changes == 0)
        let fresh = try store.resetIdentity(replacing: lost.clientId)
        #expect(changes == 1)
        #expect(fresh != local)
        #expect(try store.identity() == fresh)
        #expect(try keychain.data(for: DeviceStore.account(clientId: local.clientId)) != nil)
        #expect(defaults.object(forKey: DeviceStore.missingSinceKey) == nil)
        #expect(store.lostIdentityPointer() == nil)
    }

    /// The reset is never offered, nor done, for an identity that works or
    /// whose key is only late: on a healthy device; before the delay; when
    /// the key arrives while the confirmation is shown.
    @Test func theResetIsRefusedUnlessTheKeyIsLost() throws {
        let healthy = DeviceStore(secureStore: InMemorySecureStore(), defaults: makeDefaults())
        let identity = try healthy.identity()
        #expect(healthy.lostIdentityPointer() == nil)
        #expect(throws: DeviceStoreError.resetNotNeeded) { try healthy.resetIdentity(replacing: identity.clientId) }
        #expect(try healthy.identity() == identity)

        let iPhoneKeychain = InMemorySecureStore()
        let shared = try DeviceStore(secureStore: iPhoneKeychain, defaults: makeDefaults()).identity()
        let macKeychain = InMemorySecureStore()
        try deliver([DeviceStore.identityAccount], from: iPhoneKeychain, to: macKeychain)
        let clock = IdentityTestClock()
        let mac = DeviceStore(secureStore: macKeychain, defaults: makeDefaults(), now: { clock.now })
        #expect(mac.lostIdentityPointer() == nil)
        clock.now += 5 * 60
        #expect(mac.lostIdentityPointer() == nil)
        #expect(throws: DeviceStoreError.resetNotNeeded) { try mac.resetIdentity(replacing: shared.clientId) }
        clock.now += 6 * 60
        #expect(mac.lostIdentityPointer() == shared.clientId)
        // The key arrives while the confirmation is shown.
        try deliver([DeviceStore.account(clientId: shared.clientId)], from: iPhoneKeychain, to: macKeychain)
        #expect(throws: DeviceStoreError.resetNotNeeded) { try mac.resetIdentity(replacing: shared.clientId) }
        #expect(try mac.identity() == shared)
    }

    /// The address noted by a connection stays on this device: it never
    /// recreates a pairing another device removed, never rewrites its
    /// replacement, and is used only with the pin it was validated with.
    @Test func aNotedAddressNeverRewritesTheSharedRecord() throws {
        let keychain = InMemorySecureStore()
        let store = DeviceStore(secureStore: keychain, defaults: makeDefaults())
        let paired = Endpoint(host: "192.168.1.42", port: 47821)
        try store.save(deviceId: deviceId, name: "Brick", model: .brick, pin: pin, endpoint: paired)
        let record = try keychain.data(for: DeviceStore.account(deviceId: deviceId))
        let moved = Endpoint(host: "192.168.1.50", port: 47821)
        store.noteEndpoint(moved, for: deviceId, pin: pin)
        #expect(store.lastEndpoint(for: deviceId) == moved)
        #expect(try keychain.data(for: DeviceStore.account(deviceId: deviceId)) == record)
        // Paired again elsewhere with another pin: the old note is ignored.
        let otherPin = Base64URL.encode(Data(repeating: 9, count: 32))
        try DeviceStore(secureStore: keychain, defaults: makeDefaults())
            .save(deviceId: deviceId, name: "Brick", model: .brick, pin: otherPin, endpoint: paired)
        #expect(store.lastEndpoint(for: deviceId) == paired)
        // A connection of the old pairing answers late: its address is
        // noted with its own pin, never used for the new pairing.
        store.noteEndpoint(moved, for: deviceId, pin: pin)
        #expect(store.lastEndpoint(for: deviceId) == paired)
        // Removed elsewhere: noting an address or the console's name
        // never brings the record back.
        try keychain.remove(account: DeviceStore.account(deviceId: deviceId))
        store.noteEndpoint(moved, for: deviceId, pin: pin)
        store.noteConsoleName("Renamed", for: deviceId)
        #expect(try keychain.data(for: DeviceStore.account(deviceId: deviceId)) == nil)
    }

    /// A Keychain read error keeps the list shown (never back to the
    /// welcome screen for a transient error).
    @Test func aReadErrorKeepsTheList() throws {
        let keychain = ArrivingKeychain()
        let store = DeviceStore(secureStore: keychain, defaults: makeDefaults())
        try store.save(deviceId: deviceId, name: "Brick", model: .brick, pin: pin)
        keychain.failsEnumeration = true
        store.reload()
        #expect(store.devices.map(\.id) == [deviceId])
        #expect(store.lastReadFailed)
        keychain.failsEnumeration = false
        store.reload()
        #expect(!store.lastReadFailed)
        #expect(store.devices.map(\.id) == [deviceId])
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

    static let link = "reminislink://pair?v=2&id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Handheld&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&k=__79_Pv6-fj39vX08_Lx8O_u7ezr6uno5-bl5OPi4eA"
    static let rl1Link = "reminislink://pair?id=5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0&n=Demo%20Handheld&s=AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8&v=1"

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
        #expect(throws: QRCodeReader.ImageError.notAPicture) { try QRCodeReader.payloads(inImageData: Data("not a picture".utf8)) }
        // Bounded: the bytes before reading, the pixels before decoding.
        #expect(throws: QRCodeReader.ImageError.tooLarge) {
            try QRCodeReader.payloads(inImageData: Data(count: QRCodeReader.maxImageBytes + 1))
        }
        // A large picture is downsampled, and still read.
        let large = try Self.png(try Self.qrImage(Self.link, scale: 60))
        #expect(try QRCodeReader.payloads(inImageData: large) == [Self.link])
        // From a file, size checked before anything is read.
        let file = FileManager.default.temporaryDirectory.appending(path: "qr-\(UUID().uuidString).png")
        try data.write(to: file)
        defer { try? FileManager.default.removeItem(at: file) }
        #expect(try QRCodeReader.payloads(inImageAt: file) == [Self.link])
    }

    /// Camera and imports share one acceptance point: the first link only,
    /// and nothing once the screen closed (a late import after Cancel).
    @Test func aPairingCodeIsAcceptedOnceAndNeverAfterClosing() {
        let acceptance = PairingCodeAcceptance()
        #expect(acceptance.accept())
        #expect(!acceptance.accept())
        let closed = PairingCodeAcceptance()
        closed.close()
        #expect(closed.isClosed)
        #expect(!closed.accept())
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

/// A Keychain where an item arrives from another device just before an add
/// (iCloud Keychain delivering between a read and a write).
final class ArrivingKeychain: SecureStore, @unchecked Sendable {
    let base = InMemorySecureStore()
    var beforeAdd: ((String) -> Void)?

    func data(for account: String) throws -> Data? { try base.data(for: account) }
    func set(_ data: Data, for account: String) throws { try base.set(data, for: account) }
    func add(_ data: Data, for account: String) throws -> Bool {
        beforeAdd?(account)
        return try base.add(data, for: account)
    }
    func replace(_ data: Data, for account: String) throws -> Bool { try base.replace(data, for: account) }
    func remove(account: String) throws { try base.remove(account: account) }
    /// The next enumerations fail (a transient Keychain error).
    var failsEnumeration = false
    func allAccounts() throws -> [String] {
        if failsEnumeration { throw KeychainError.status(-25308) }
        return try base.allAccounts()
    }
}

/// A settable clock.
final class IdentityTestClock: @unchecked Sendable {
    var now = Date(timeIntervalSince1970: 1_000_000)
}
