import CryptoKit
import Foundation
import Observation

/// A paired device as listed by the app. The secrets live in the
/// ``SecureStore``; this part goes to the app group `UserDefaults`.
public struct PairedDevice: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    /// Display name, renamable on the app side.
    public var name: String
    public var model: DeviceModel
    public let pairedAt: Date

    public init(id: String, name: String, model: DeviceModel, pairedAt: Date) {
        self.id = id
        self.name = name
        self.model = model
        self.pairedAt = pairedAt
    }
}

public enum DeviceStoreError: Error, Equatable, Sendable {
    case invalidName
}

/// Paired devices and their credentials (docs/specs/06-app-ios.md §6).
///
/// - Keychain: the installation `clientId`, and for each device its id,
///   the client id and the key.
/// - `UserDefaults` of the app group: display names and models.
@MainActor
@Observable
public final class DeviceStore {
    /// Longest display name.
    public static let maxNameLength = 64
    static let defaultsKey = "pairedDevices"
    static let clientIdAccount = "client-id"
    static func account(deviceId: String) -> String { "device." + deviceId }

    public private(set) var devices: [PairedDevice] = []

    @ObservationIgnored private let secureStore: SecureStore
    @ObservationIgnored private let defaults: UserDefaults

    public init(secureStore: SecureStore, defaults: UserDefaults) {
        self.secureStore = secureStore
        self.defaults = defaults
        load()
    }

    /// The `clientId` of this installation, created on first use: a
    /// lowercase UUID, stable across pairings.
    public func installationClientId() throws -> String {
        if let data = try secureStore.data(for: Self.clientIdAccount),
           let id = String(data: data, encoding: .utf8), PairingLink.isLowercaseUUID(id) {
            return id
        }
        let id = Pairing.makeClientId()
        try secureStore.set(Data(id.utf8), for: Self.clientIdAccount)
        return id
    }

    /// Stores a new pairing, or replaces the key of a device paired again.
    @discardableResult
    public func save(deviceId: String, name: String, model: DeviceModel, clientId: String, key: SymmetricKey, now: Date = Date()) throws -> PairedDevice {
        let stored = StoredCredentials(deviceId: deviceId, clientId: clientId, key: key.withUnsafeBytes { Base64URL.encode(Data($0)) })
        try secureStore.set(try JSONEncoder().encode(stored), for: Self.account(deviceId: deviceId))
        let cleanName = Self.clean(name) ?? model.displayName
        let device: PairedDevice
        if let index = devices.firstIndex(where: { $0.id == deviceId }) {
            devices[index].model = model
            device = devices[index]
        } else {
            device = PairedDevice(id: deviceId, name: cleanName, model: model, pairedAt: now)
            devices.append(device)
        }
        persist()
        return device
    }

    /// The credentials of a device, nil if the Keychain has none.
    public func credentials(for deviceId: String) throws -> DeviceCredentials? {
        guard let data = try secureStore.data(for: Self.account(deviceId: deviceId)) else { return nil }
        guard let stored = try? JSONDecoder().decode(StoredCredentials.self, from: data),
              stored.deviceId == deviceId,
              let key = Base64URL.decode(stored.key), key.count == Pairing.keySize
        else { throw KeychainError.unexpectedData }
        return DeviceCredentials(deviceId: deviceId, clientId: stored.clientId, key: SymmetricKey(data: key))
    }

    public func device(id: String) -> PairedDevice? {
        devices.first { $0.id == id }
    }

    /// Renames a device on the app side (1 to 64 characters, trimmed).
    public func rename(_ deviceId: String, to name: String) throws {
        guard let clean = Self.clean(name) else { throw DeviceStoreError.invalidName }
        guard let index = devices.firstIndex(where: { $0.id == deviceId }) else { return }
        devices[index].name = clean
        persist()
    }

    /// Forgets a device: its key and its entry.
    public func remove(_ deviceId: String) throws {
        try secureStore.remove(account: Self.account(deviceId: deviceId))
        devices.removeAll { $0.id == deviceId }
        persist()
    }

    /// Trims and checks a name; nil if empty or too long once trimmed.
    public static func clean(_ name: String) -> String? {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed.count <= maxNameLength else { return nil }
        return trimmed
    }

    private func load() {
        guard let data = defaults.data(forKey: Self.defaultsKey),
              let list = try? JSONDecoder().decode([PairedDevice].self, from: data)
        else { devices = []; return }
        devices = list
    }

    private func persist() {
        if let data = try? JSONEncoder().encode(devices) {
            defaults.set(data, forKey: Self.defaultsKey)
        }
    }

    private struct StoredCredentials: Codable {
        let deviceId: String
        let clientId: String
        let key: String
    }
}
