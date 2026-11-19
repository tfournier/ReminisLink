import CryptoKit
import Foundation
import Security
import Testing
@testable import ReminisLinkKit

@MainActor
@Suite("Stores")
struct StoreTests {
    let deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"

    func makeDefaults() -> UserDefaults {
        let suite = "ReminisLinkKitTests.\(UUID().uuidString)"
        let defaults = UserDefaults(suiteName: suite)!
        defaults.removePersistentDomain(forName: suite)
        return defaults
    }

    @Test func installationClientIdIsStable() throws {
        let secure = InMemorySecureStore()
        let store = DeviceStore(secureStore: secure, defaults: makeDefaults())
        let id = try store.installationClientId()
        #expect(PairingLink.isLowercaseUUID(id))
        #expect(try store.installationClientId() == id)
        #expect(try DeviceStore(secureStore: secure, defaults: makeDefaults()).installationClientId() == id)
    }

    /// M11: the console renamed on the pak replaces the display name, by
    /// device id; a name chosen on the app stays until the console's
    /// name changes.
    @Test func aNameChangedOnTheConsoleIsShown() throws {
        let secure = InMemorySecureStore()
        let defaults = makeDefaults()
        let store = DeviceStore(secureStore: secure, defaults: defaults)
        let other = "1f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a11"
        let key = SymmetricKey(size: .bits256)
        let clientId = try store.installationClientId()
        // Renamed on the app at pairing.
        try store.save(deviceId: deviceId, name: "My Brick", model: .brick, clientId: clientId, key: key, consoleName: "Brick 3a10")
        try store.save(deviceId: other, name: "Brick 3a10", model: .brick, clientId: clientId, key: key)
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
        // A device stored before M11 (no console name) takes the console's.
        #expect(store.device(id: other)?.consoleName == nil)
        #expect(store.noteConsoleName("Bedroom", for: other))
        #expect(store.device(id: other)?.name == "Bedroom")
    }

    @Test func aDeviceStoredBeforeM11Decodes() throws {
        let defaults = makeDefaults()
        let json = #"[{"id":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","name":"Brick","model":"brick","pairedAt":0}]"#
        defaults.set(Data(json.utf8), forKey: DeviceStore.defaultsKey)
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: defaults)
        #expect(store.device(id: deviceId)?.name == "Brick")
        #expect(store.device(id: deviceId)?.consoleName == nil)
    }

    @Test func saveLoadRenameRemove() throws {
        let secure = InMemorySecureStore()
        let defaults = makeDefaults()
        let store = DeviceStore(secureStore: secure, defaults: defaults)
        let key = SymmetricKey(size: .bits256)
        let clientId = try store.installationClientId()
        try store.save(deviceId: deviceId, name: "  Brick de Thomas ", model: .brick, clientId: clientId, key: key)
        #expect(store.devices.map(\.name) == ["Brick de Thomas"])

        let credentials = try #require(try store.credentials(for: deviceId))
        #expect(credentials.clientId == clientId)
        #expect(credentials.key == key)

        // Display data is in the defaults, secrets are not.
        let reloaded = DeviceStore(secureStore: secure, defaults: defaults)
        #expect(reloaded.devices.first?.id == deviceId)
        let raw = try #require(defaults.data(forKey: DeviceStore.defaultsKey))
        let keyText = key.withUnsafeBytes { Base64URL.encode(Data($0)) }
        #expect(!String(decoding: raw, as: UTF8.self).contains(keyText))

        try store.rename(deviceId, to: "Ma Brick")
        #expect(store.device(id: deviceId)?.name == "Ma Brick")
        #expect(throws: DeviceStoreError.invalidName) { try store.rename(deviceId, to: "   ") }
        #expect(throws: DeviceStoreError.invalidName) { try store.rename(deviceId, to: String(repeating: "a", count: 65)) }

        // Pairing again replaces the key, keeps the name.
        let newKey = SymmetricKey(size: .bits256)
        try store.save(deviceId: deviceId, name: "Other", model: .brickPro, clientId: clientId, key: newKey)
        #expect(store.devices.count == 1)
        #expect(store.device(id: deviceId)?.name == "Ma Brick")
        #expect(try store.credentials(for: deviceId)?.key == newKey)

        try store.remove(deviceId)
        #expect(store.devices.isEmpty)
        #expect(try store.credentials(for: deviceId) == nil)
        #expect(try secure.allAccounts() == [DeviceStore.clientIdAccount])
    }

    @Test func emptyNameFallsBackToTheModel() throws {
        let store = DeviceStore(secureStore: InMemorySecureStore(), defaults: makeDefaults())
        try store.save(deviceId: deviceId, name: "", model: .brick, clientId: Pairing.makeClientId(), key: SymmetricKey(size: .bits256))
        #expect(store.devices.first?.name == "TrimUI Brick")
    }

    @Test func corruptedCredentialsAreReported() throws {
        let secure = InMemorySecureStore()
        try secure.set(Data("garbage".utf8), for: DeviceStore.account(deviceId: deviceId))
        let store = DeviceStore(secureStore: secure, defaults: makeDefaults())
        #expect(throws: KeychainError.unexpectedData) { try store.credentials(for: deviceId) }
    }

    @Test func keychainItemsStayOnThisDevice() {
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
}
