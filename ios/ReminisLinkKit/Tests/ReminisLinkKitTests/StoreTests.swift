import CryptoKit
import Foundation
import Security
import Testing
@testable import ReminisLinkKit

@MainActor
@Suite("Stores")
struct StoreTests {
    let deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    let pin = Base64URL.encode(Data(repeating: 3, count: 32))

    func makeDefaults() -> UserDefaults {
        let suite = "ReminisLinkKitTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        return defaults
    }

    @Test func identityIsCreatedOnceAndShared() throws {
        let secure = InMemorySecureStore()
        let store = DeviceStore(secureStore: secure, defaults: makeDefaults())
        let identity = try store.identity()
        #expect(PairingLink.isLowercaseUUID(identity.clientId))
        #expect(try store.identity() == identity)
        // Another device of the Apple account reads the same item.
        #expect(try DeviceStore(secureStore: secure, defaults: makeDefaults()).identity() == identity)
        // One item for the client id and the private key together, and
        // the pointer to it.
        #expect(try secure.allAccounts() == [DeviceStore.identityAccount, DeviceStore.account(clientId: identity.clientId)])
    }

    @Test func pairingNameIsKeptWithTheIdentity() throws {
        let secure = InMemorySecureStore()
        let store = DeviceStore(secureStore: secure, defaults: makeDefaults())
        #expect(store.pairingName() == nil)
        let identity = try store.identity()
        try store.setPairingName("  Thomas's Apple devices ")
        #expect(store.pairingName() == "Thomas's Apple devices")
        #expect(try store.identity() == identity)
        #expect(DeviceStore(secureStore: secure, defaults: makeDefaults()).pairingName() == "Thomas's Apple devices")
    }

    @Test func corruptedIdentityIsReported() throws {
        let secure = InMemorySecureStore()
        try secure.set(Data("garbage".utf8), for: DeviceStore.identityAccount)
        let store = DeviceStore(secureStore: secure, defaults: makeDefaults())
        #expect(throws: KeychainError.unexpectedData) { try store.identity() }
    }

    /// M11: the console renamed on the pak replaces the display name, by
    /// device id; a name chosen on the app stays until the console's
    /// name changes.
    @Test func aNameChangedOnTheConsoleIsShown() throws {
        let secure = InMemorySecureStore()
        let defaults = makeDefaults()
        let store = DeviceStore(secureStore: secure, defaults: defaults)
        let other = "1f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a11"
        // Renamed on the app at pairing.
        try store.save(deviceId: deviceId, name: "My Brick", model: .brick, pin: pin, consoleName: "Brick 3a10")
        try store.save(deviceId: other, name: "Brick 3a10", model: .brick, pin: pin)
        #expect(!store.noteConsoleName("Brick 3a10", for: deviceId))
        #expect(store.device(id: deviceId)?.name == "My Brick")
        // Renamed on the console: shown, and kept across launches.
        #expect(store.noteConsoleName("  Living Room ", for: deviceId))
        #expect(store.device(id: deviceId)?.name == "Living Room")
        #expect(store.device(id: other)?.name == "Brick 3a10")
        #expect(DeviceStore(secureStore: secure, defaults: defaults).device(id: deviceId)?.name == "Living Room")
        // Never by name, never an invalid name, never an unknown id.
        #expect(!store.noteConsoleName("", for: deviceId))
        #expect(!store.noteConsoleName("X", for: "unknown"))
        #expect(store.device(id: deviceId)?.name == "Living Room")
        // A device stored before M11 (no console name): the first name
        // reported is shown (the list shows the console's name), and so is
        // a later change.
        #expect(store.device(id: other)?.consoleName == nil)
        #expect(store.noteConsoleName("Bedroom", for: other))
        #expect(store.device(id: other)?.name == "Bedroom")
        #expect(store.device(id: other)?.consoleName == "Bedroom")
        #expect(!store.noteConsoleName("Bedroom", for: other))
        #expect(store.noteConsoleName("Attic", for: other))
        #expect(store.device(id: other)?.name == "Attic")
    }

    /// Paired again after a rename on the console: the new name is shown,
    /// while a name chosen in the app survives a pairing without rename.
    @Test func pairingAgainShowsANameChangedOnTheConsole() throws {
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: makeDefaults())
        try store.save(deviceId: deviceId, name: "Salon", model: .brick, pin: pin, consoleName: "Salon")
        // Renamed on the console, paired again before reopening it.
        try store.save(deviceId: deviceId, name: "Bureau", model: .brick, pin: pin, consoleName: "Bureau")
        #expect(store.device(id: deviceId)?.name == "Bureau")
        #expect(!store.noteConsoleName("Bureau", for: deviceId))
        // A name chosen in the app, paired again without a rename: kept.
        try store.rename(deviceId, to: "My Brick")
        try store.save(deviceId: deviceId, name: "Bureau", model: .brick, pin: pin, consoleName: "Bureau")
        #expect(store.device(id: deviceId)?.name == "My Brick")
        // Stored before M11 (no console name), paired again: the console's
        // current name is shown.
        let migrated = "4b1c9e1f-6a1e-4f0e-9a51-2d8c7b9e0a13"
        try store.save(deviceId: migrated, name: "Brick deThomas", model: .brick, pin: pin)
        try store.save(deviceId: migrated, name: "Brick deThomas", model: .brick, pin: pin, consoleName: "Brick de Thomas")
        #expect(store.device(id: migrated)?.name == "Brick de Thomas")
        #expect(store.device(id: migrated)?.consoleName == "Brick de Thomas")
    }


    /// M13: a console paired before M13 (RL1) stays listed, marked
    /// "needs re-pairing"; its key is deleted. Pairing it again clears the
    /// mark; forgetting it removes it.
    @Test func rl1PairingsNeedRepairing() throws {
        let defaults = makeDefaults()
        let json = #"[{"id":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","name":"Brick","model":"brick","pairedAt":0},"#
            + #"{"id":"1f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a11","name":"Other","model":"brick","pairedAt":1,"consoleName":"Other"}]"#
        defaults.set(Data(json.utf8), forKey: DeviceStore.defaultsKey)
        let legacyKeys = InMemorySecureStore()
        try legacyKeys.set(Data("id".utf8), for: "client-id")
        try legacyKeys.set(Data("key".utf8), for: "device.7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10")
        let secure = InMemorySecureStore()
        let store = DeviceStore(secureStore: secure, defaults: defaults, legacySecureStore: legacyKeys)
        #expect(try legacyKeys.allAccounts().isEmpty)
        #expect(store.devices.map(\.name) == ["Brick", "Other"])
        #expect(store.devices.allSatisfy { $0.needsRepair })
        #expect(store.device(id: deviceId)?.consoleName == nil)
        #expect(try store.credentials(for: deviceId) == nil)
        // Renamed while waiting: kept.
        try store.rename(deviceId, to: "My Brick")
        #expect(DeviceStore(secureStore: secure, defaults: defaults).device(id: deviceId)?.name == "My Brick")

        // Paired again: the entry moves to the synchronized Keychain.
        _ = try store.identity()
        try store.save(deviceId: deviceId, name: "Brick", model: .brick, pin: pin, consoleName: "Brick")
        #expect(store.device(id: deviceId)?.needsRepair == false)
        #expect(store.device(id: deviceId)?.name == "Brick")
        #expect(try store.credentials(for: deviceId)?.pin == pin)
        #expect(store.devices.count == 2)
        let reloaded = DeviceStore(secureStore: secure, defaults: defaults)
        #expect(reloaded.device(id: deviceId)?.needsRepair == false)
        #expect(reloaded.device(id: "1f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a11")?.needsRepair == true)

        // Forgotten: gone, and the defaults are emptied.
        try store.remove("1f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a11")
        #expect(store.devices.map(\.id) == [deviceId])
        #expect(defaults.data(forKey: DeviceStore.defaultsKey) == nil)
    }

    /// The consoles paired on another device of the Apple account appear
    /// after a reload (iCloud Keychain).
    @Test func consolesPairedElsewhereAppearOnReload() throws {
        let secure = InMemorySecureStore()
        let iPhone = DeviceStore(secureStore: secure, defaults: makeDefaults())
        let iPad = DeviceStore(secureStore: secure, defaults: makeDefaults())
        _ = try iPhone.identity()
        try iPhone.save(deviceId: deviceId, name: "Brick", model: .brick, pin: pin)
        #expect(iPad.devices.isEmpty)
        iPad.reload()
        #expect(iPad.devices.map(\.id) == [deviceId])
        let identity = try iPhone.identity()
        #expect(try iPad.credentials(for: deviceId)?.clientId == identity.clientId)
        try iPad.remove(deviceId)
        iPhone.reload()
        #expect(iPhone.devices.isEmpty)
    }

    @Test func saveLoadRenameRemove() throws {
        let secure = InMemorySecureStore()
        let defaults = makeDefaults()
        let store = DeviceStore(secureStore: secure, defaults: defaults)
        let identity = try store.identity()
        try store.save(deviceId: deviceId, name: "  Brick de Thomas ", model: .brick, pin: pin)
        #expect(store.devices.map(\.name) == ["Brick de Thomas"])
        #expect(store.devices.first?.needsRepair == false)

        let credentials = try #require(try store.credentials(for: deviceId))
        #expect(credentials.identity == identity)
        #expect(credentials.pin == pin)

        // Everything is in the (synchronized) Keychain, nothing in the defaults.
        let reloaded = DeviceStore(secureStore: secure, defaults: makeDefaults())
        #expect(reloaded.devices.first?.id == deviceId)
        #expect(defaults.data(forKey: DeviceStore.defaultsKey) == nil)

        try store.rename(deviceId, to: "Ma Brick")
        #expect(store.device(id: deviceId)?.name == "Ma Brick")
        #expect(throws: DeviceStoreError.invalidName) { try store.rename(deviceId, to: "   ") }
        #expect(throws: DeviceStoreError.invalidName) { try store.rename(deviceId, to: String(repeating: "a", count: 65)) }

        // Pairing again replaces the pin, keeps the name.
        let newPin = Base64URL.encode(Data(repeating: 4, count: 32))
        try store.save(deviceId: deviceId, name: "Other", model: .brickPro, pin: newPin)
        #expect(store.devices.count == 1)
        #expect(store.device(id: deviceId)?.name == "Ma Brick")
        #expect(try store.credentials(for: deviceId)?.pin == newPin)

        try store.remove(deviceId)
        #expect(store.devices.isEmpty)
        #expect(try store.credentials(for: deviceId) == nil)
        #expect(try secure.allAccounts() == [DeviceStore.identityAccount, DeviceStore.account(clientId: identity.clientId)])
    }

    @Test func emptyNameFallsBackToTheModel() throws {
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: makeDefaults())
        try store.save(deviceId: deviceId, name: "", model: .brick, pin: pin)
        #expect(store.devices.first?.name == "TrimUI Brick")
    }

    @Test func corruptedCredentialsAreReported() throws {
        let secure = InMemorySecureStore()
        try secure.set(Data("garbage".utf8), for: DeviceStore.account(deviceId: deviceId))
        let store = DeviceStore(secureStore: secure, defaults: makeDefaults())
        #expect(throws: KeychainError.unexpectedData) { try store.credentials(for: deviceId) }
    }

    /// Two devices of one Apple account each create an identity and pair
    /// a console before the iCloud Keychain converges: once merged (the
    /// pointer of one wins), each console still signs with the identity it
    /// was paired with, and neither identity item is lost.
    @Test func divergingIdentitiesConvergeWithoutBreakingPairings() throws {
        let other = "1f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a11"
        let keychainA = InMemorySecureStore()
        let keychainB = InMemorySecureStore()
        let iPhone = DeviceStore(secureStore: keychainA, defaults: makeDefaults())
        let iPad = DeviceStore(secureStore: keychainB, defaults: makeDefaults())
        let identityA = try iPhone.identity()
        let identityB = try iPad.identity()
        #expect(identityA != identityB)
        try iPhone.save(deviceId: deviceId, name: "X", model: .brick, clientId: identityA.clientId, pin: pin)
        try iPad.save(deviceId: other, name: "Y", model: .brick, clientId: identityB.clientId, pin: pin)

        // The Keychain converges: every item of B reaches A, B's pointer
        // wins (last writer), and the other way round.
        for account in try keychainB.allAccounts() {
            let data = try #require(try keychainB.data(for: account))
            try keychainA.set(data, for: account)
        }
        for account in try keychainA.allAccounts() where try keychainB.data(for: account) == nil {
            let data = try #require(try keychainA.data(for: account))
            try keychainB.set(data, for: account)
        }
        for store in [iPhone, iPad] {
            store.reload()
            #expect(Set(store.devices.map(\.id)) == [deviceId, other])
            #expect(try store.credentials(for: deviceId)?.identity == identityA)
            #expect(try store.credentials(for: other)?.identity == identityB)
            // New pairings use the identity the pointer settled on.
            #expect(try store.identity() == identityB)
        }

        // Forgetting X leaves identity A unused: it is deleted, not B.
        try iPhone.remove(deviceId)
        #expect(try keychainA.data(for: DeviceStore.account(clientId: identityA.clientId)) == nil)
        #expect(try iPhone.credentials(for: other)?.identity == identityB)
    }

    /// An identity item that has not arrived yet: the console is listed
    /// but has no credentials (no request signed with another identity).
    @Test func aConsoleWhoseIdentityHasNotArrivedHasNoCredentials() throws {
        let secure = InMemorySecureStore()
        let store = DeviceStore(secureStore: secure, defaults: makeDefaults())
        let identity = try store.identity()
        try store.save(deviceId: deviceId, name: "X", model: .brick, pin: pin)
        try secure.remove(account: DeviceStore.account(clientId: identity.clientId))
        #expect(try store.credentials(for: deviceId) == nil)
    }

    @Test func theLastValidatedAddressIsKept() throws {
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: makeDefaults())
        let qr = Endpoint(host: "192.168.1.42", port: 47821)
        try store.save(deviceId: deviceId, name: "X", model: .brick, pin: pin, endpoint: qr)
        #expect(store.lastEndpoint(for: deviceId) == qr)
        let moved = Endpoint(host: "192.168.1.50", port: 47822)
        store.noteEndpoint(moved, for: deviceId)
        #expect(store.lastEndpoint(for: deviceId) == moved)
        #expect(store.lastEndpoint(for: "unknown") == nil)
    }

    @Test func legacyKeychainItemsStayOnThisDevice() {
        let store = KeychainStore(service: "test.service", accessGroup: "group.test")
        let query = store.baseQuery(account: "a")
        #expect(query[kSecClass as String] as? String == kSecClassGenericPassword as String)
        #expect(query[kSecAttrService as String] as? String == "test.service")
        #expect(query[kSecAttrAccount as String] as? String == "a")
        #expect(query[kSecAttrAccessGroup as String] as? String == "group.test")
        #expect(query[kSecAttrSynchronizable as String] as? Bool == false)
        let attributes = store.writeAttributes(data: Data([1]))
        #expect(attributes[kSecAttrAccessible as String] as? String == kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly as String)
        #expect(KeychainStore(service: "s").baseQuery(account: nil)[kSecAttrAccessGroup as String] == nil)
    }

    /// M13: the identity and the consoles are synchronizable items (iCloud
    /// Keychain), readable after the first unlock, in the shared group.
    /// Round trip through an injected Keychain that checks every query.
    @Test func synchronizableItemsRoundTrip() throws {
        let fake = FakeKeychain()
        let keychain = KeychainStore(service: "ReminisLink.pairing", accessGroup: "group.test", synchronizable: true, api: fake.api)
        let store = DeviceStore(secureStore: keychain, defaults: makeDefaults())
        let identity = try store.identity()
        try store.save(deviceId: deviceId, name: "Brick", model: .brick, pin: pin)

        // A second store reading the same Keychain (another device).
        let other = DeviceStore(secureStore: keychain, defaults: makeDefaults())
        #expect(try other.identity() == identity)
        #expect(try other.credentials(for: deviceId)?.pin == pin)
        #expect(other.devices.map(\.id) == [deviceId])

        #expect(!fake.queries.isEmpty)
        for query in fake.queries {
            #expect(query[kSecAttrSynchronizable as String] as? Bool == true)
            #expect(query[kSecAttrAccessGroup as String] as? String == "group.test")
            #expect(query[kSecAttrService as String] as? String == "ReminisLink.pairing")
        }
        #expect(!fake.written.isEmpty)
        for attributes in fake.written {
            #expect(attributes[kSecAttrAccessible as String] as? String == kSecAttrAccessibleAfterFirstUnlock as String)
        }
        let identityItem = DeviceStore.account(clientId: identity.clientId)
        #expect(fake.accounts == [DeviceStore.account(deviceId: deviceId), DeviceStore.identityAccount, identityItem])
        try store.remove(deviceId)
        #expect(fake.accounts == [DeviceStore.identityAccount, identityItem])
    }
}

/// An in-memory Keychain behind ``KeychainAPI``: records every query and
/// every written attribute set.
final class FakeKeychain: @unchecked Sendable {
    private let lock = NSLock()
    private var items: [String: Data] = [:]
    private var _queries: [[String: Any]] = []
    private var _written: [[String: Any]] = []

    var accounts: [String] { lock.withLock { items.keys.sorted() } }
    var queries: [[String: Any]] { lock.withLock { _queries } }
    var written: [[String: Any]] { lock.withLock { _written } }

    var api: KeychainAPI {
        KeychainAPI(
            copyMatching: { [self] query, result in
                lock.withLock {
                    _queries.append(query)
                    if let account = query[kSecAttrAccount as String] as? String {
                        guard let data = items[account] else { return errSecItemNotFound }
                        result?.pointee = data as CFData
                        return errSecSuccess
                    }
                    guard !items.isEmpty else { return errSecItemNotFound }
                    result?.pointee = items.keys.map { [kSecAttrAccount as String: $0] } as CFArray
                    return errSecSuccess
                }
            },
            add: { [self] attributes in
                lock.withLock {
                    _queries.append(attributes)
                    _written.append(attributes)
                    guard let account = attributes[kSecAttrAccount as String] as? String,
                          let data = attributes[kSecValueData as String] as? Data
                    else { return errSecParam }
                    guard items[account] == nil else { return errSecDuplicateItem }
                    items[account] = data
                    return errSecSuccess
                }
            },
            update: { [self] query, attributes in
                lock.withLock {
                    _queries.append(query)
                    _written.append(attributes)
                    guard let account = query[kSecAttrAccount as String] as? String, items[account] != nil else {
                        return errSecItemNotFound
                    }
                    items[account] = attributes[kSecValueData as String] as? Data
                    return errSecSuccess
                }
            },
            delete: { [self] query in
                lock.withLock {
                    _queries.append(query)
                    guard let account = query[kSecAttrAccount as String] as? String,
                          items.removeValue(forKey: account) != nil
                    else { return errSecItemNotFound }
                    return errSecSuccess
                }
            }
        )
    }
}
