import CryptoKit
import Foundation
import Observation

/// A paired device as listed by the app.
public struct PairedDevice: Codable, Identifiable, Hashable, Sendable {
    public let id: String
    /// Display name, renamable on the app side.
    public var name: String
    public var model: DeviceModel
    public let pairedAt: Date
    /// The name the console last reported (`deviceName` of `/hello` or
    /// `/device`), nil before it was recorded (paired before M11).
    public var consoleName: String?
    /// Paired before M13 (RL1, shared HMAC key): the key was dropped, the
    /// console must be paired again by scanning its QR code.
    public var needsRepair: Bool

    public init(
        id: String, name: String, model: DeviceModel, pairedAt: Date, consoleName: String? = nil, needsRepair: Bool = false
    ) {
        self.id = id
        self.name = name
        self.model = model
        self.pairedAt = pairedAt
        self.consoleName = consoleName
        self.needsRepair = needsRepair
    }

    public init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        id = try c.decode(String.self, forKey: .id)
        name = try c.decode(String.self, forKey: .name)
        model = try c.decode(DeviceModel.self, forKey: .model)
        pairedAt = try c.decode(Date.self, forKey: .pairedAt)
        consoleName = try c.decodeIfPresent(String.self, forKey: .consoleName)
        // Entries stored before M13 have no flag: read as RL1 pairings.
        needsRepair = try c.decodeIfPresent(Bool.self, forKey: .needsRepair) ?? true
    }
}

public enum DeviceStoreError: Error, Equatable, Sendable {
    case invalidName
}

/// Paired devices and their credentials (docs/specs/06-app-ios.md §6, M13).
///
/// - Synchronized Keychain (iCloud Keychain, ``secureStore``): the client
///   identity (`clientId` and Ed25519 private key, one item) and, for each
///   console, its id, certificate pin, names and model. The iPhone, iPad
///   and Mac of one Apple account share them: they pair once.
/// - `UserDefaults` of the app group: only the consoles paired before M13
///   (RL1), kept in the list as "needs re-pairing" until they are paired
///   again or forgotten. Their keys are deleted (``legacySecureStore``).
@MainActor
@Observable
public final class DeviceStore {
    /// Longest display name.
    public static let maxNameLength = 64
    static let defaultsKey = "pairedDevices"
    static let identityAccount = "identity"
    static let consolePrefix = "console."
    static func account(deviceId: String) -> String { consolePrefix + deviceId }

    public private(set) var devices: [PairedDevice] = []

    @ObservationIgnored private let secureStore: SecureStore
    @ObservationIgnored private let defaults: UserDefaults
    /// RL1 pairings to drop (not synchronized).
    @ObservationIgnored private let legacySecureStore: SecureStore?
    /// Consoles paired before M13, waiting to be paired again.
    @ObservationIgnored private var legacy: [PairedDevice] = []

    public init(secureStore: SecureStore, defaults: UserDefaults, legacySecureStore: SecureStore? = nil) {
        self.secureStore = secureStore
        self.defaults = defaults
        self.legacySecureStore = legacySecureStore
        migrateRL1()
        reload()
    }

    // MARK: - Identity

    /// The identity of this app, shared through the iCloud Keychain;
    /// created on first use.
    public func identity() throws -> ClientIdentity {
        if let record = try identityRecord() { return record.identity }
        let identity = ClientIdentity.generate()
        try writeIdentity(IdentityRecord(identity: identity, name: nil))
        return identity
    }

    /// The name of the identity shown on the consoles (`clientName`), as
    /// last typed; nil before. Shared like the identity.
    public func pairingName() -> String? {
        (try? identityRecord())?.flatMap(\.name)
    }

    /// Records the name typed for the identity (sent at the next pairing).
    public func setPairingName(_ name: String) throws {
        let identity = try identity()
        try writeIdentity(IdentityRecord(identity: identity, name: Self.clean(name)))
    }

    private func identityRecord() throws -> IdentityRecord? {
        guard let data = try secureStore.data(for: Self.identityAccount) else { return nil }
        guard let record = try? JSONDecoder().decode(IdentityRecord.self, from: data), record.isValid else {
            throw KeychainError.unexpectedData
        }
        return record
    }

    private func writeIdentity(_ record: IdentityRecord) throws {
        try secureStore.set(try JSONEncoder().encode(record), for: Self.identityAccount)
    }

    // MARK: - Consoles

    /// Stores a new pairing, or replaces the pin of a console paired again.
    @discardableResult
    public func save(
        deviceId: String, name: String, model: DeviceModel, pin: String,
        consoleName: String? = nil, now: Date = Date()
    ) throws -> PairedDevice {
        let reported = consoleName.flatMap(Self.clean)
        var device: PairedDevice
        if let existing = devices.first(where: { $0.id == deviceId }) {
            device = existing
            device.model = model
            device.needsRepair = false
            // Paired again: the console's name is shown when it changed
            // since, or when none was recorded yet (stored before M11), as
            // by ``noteConsoleName(_:for:)``.
            if let reported {
                if device.consoleName != reported { device.name = reported }
                device.consoleName = reported
            }
        } else {
            device = PairedDevice(
                id: deviceId, name: Self.clean(name) ?? model.displayName, model: model, pairedAt: now,
                consoleName: reported, needsRepair: false
            )
        }
        try write(ConsoleRecord(device: device, pin: pin))
        legacy.removeAll { $0.id == deviceId }
        persistLegacy()
        reload()
        return device
    }

    /// The credentials of a device: the identity and the console's pin.
    /// Nil if the console is not paired with this identity (never paired,
    /// or paired before M13).
    public func credentials(for deviceId: String) throws -> DeviceCredentials? {
        guard let record = try consoleRecord(deviceId) else { return nil }
        guard let identity = try identityRecord()?.identity else { return nil }
        return DeviceCredentials(deviceId: deviceId, identity: identity, pin: record.pin)
    }

    public func device(id: String) -> PairedDevice? {
        devices.first { $0.id == id }
    }

    /// Renames a device on the app side (1 to 64 characters, trimmed).
    public func rename(_ deviceId: String, to name: String) throws {
        guard let clean = Self.clean(name) else { throw DeviceStoreError.invalidName }
        try update(deviceId) { $0.name = clean }
    }

    /// The console with this id reported its name (M11: renamed on the
    /// console, Settings > Console name). A name the console changed
    /// replaces the display name, so the list shows it; a name chosen on
    /// the app stays until the console's name changes. Matched by id only,
    /// never by name. A device stored before M11 (no console name yet)
    /// takes the first name reported: the list shows the console's name.
    /// Returns whether the display name changed.
    @discardableResult
    public func noteConsoleName(_ name: String, for deviceId: String) -> Bool {
        guard let reported = Self.clean(name), let device = device(id: deviceId), device.consoleName != reported else {
            return false
        }
        let changed = device.name != reported
        try? update(deviceId) {
            $0.consoleName = reported
            $0.name = reported
        }
        return changed
    }

    /// Forgets a device: its pairing (on every device of the Apple account)
    /// and its entry.
    public func remove(_ deviceId: String) throws {
        try secureStore.remove(account: Self.account(deviceId: deviceId))
        legacy.removeAll { $0.id == deviceId }
        persistLegacy()
        reload()
    }

    /// Reads the list again: another device of the Apple account may have
    /// paired or forgotten a console (iCloud Keychain).
    public func reload() {
        var list: [PairedDevice] = []
        let accounts = (try? secureStore.allAccounts()) ?? []
        for account in accounts where account.hasPrefix(Self.consolePrefix) {
            let id = String(account.dropFirst(Self.consolePrefix.count))
            guard let record = try? consoleRecord(id) else { continue }
            var device = record.device
            device.needsRepair = false
            list.append(device)
        }
        let paired = Set(list.map(\.id))
        list += legacy.filter { !paired.contains($0.id) }
        list.sort { $0.pairedAt != $1.pairedAt ? $0.pairedAt < $1.pairedAt : $0.id < $1.id }
        if list != devices { devices = list }
    }

    /// Trims and checks a name; nil if empty or too long once trimmed.
    public static func clean(_ name: String) -> String? {
        let trimmed = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty, trimmed.count <= maxNameLength else { return nil }
        return trimmed
    }

    // MARK: - Storage

    private func consoleRecord(_ deviceId: String) throws -> ConsoleRecord? {
        guard let data = try secureStore.data(for: Self.account(deviceId: deviceId)) else { return nil }
        guard let record = try? JSONDecoder().decode(ConsoleRecord.self, from: data), record.device.id == deviceId,
              Base64URL.decode(record.pin)?.count == TLSPin.size
        else { throw KeychainError.unexpectedData }
        return record
    }

    private func write(_ record: ConsoleRecord) throws {
        try secureStore.set(try JSONEncoder().encode(record), for: Self.account(deviceId: record.device.id))
    }

    /// Changes the entry of a device, paired with this identity or waiting
    /// to be paired again.
    private func update(_ deviceId: String, _ change: (inout PairedDevice) -> Void) throws {
        if var record = try consoleRecord(deviceId) {
            change(&record.device)
            try write(record)
        } else if let index = legacy.firstIndex(where: { $0.id == deviceId }) {
            change(&legacy[index])
            persistLegacy()
        }
        reload()
    }

    /// M13: the RL1 keys are deleted (they no longer sign anything a pak
    /// accepts); their consoles stay listed, to pair again.
    private func migrateRL1() {
        if let legacySecureStore, let accounts = try? legacySecureStore.allAccounts() {
            for account in accounts { try? legacySecureStore.remove(account: account) }
        }
        guard let data = defaults.data(forKey: Self.defaultsKey),
              let list = try? JSONDecoder().decode([PairedDevice].self, from: data)
        else { legacy = []; return }
        legacy = list.map {
            var device = $0
            device.needsRepair = true
            return device
        }
    }

    private func persistLegacy() {
        if legacy.isEmpty {
            defaults.removeObject(forKey: Self.defaultsKey)
        } else if let data = try? JSONEncoder().encode(legacy) {
            defaults.set(data, forKey: Self.defaultsKey)
        }
    }

    /// The identity item.
    private struct IdentityRecord: Codable {
        let clientId: String
        /// The 32-byte Ed25519 private key (raw representation), base64url.
        let privateKey: String
        let name: String?

        init(identity: ClientIdentity, name: String?) {
            clientId = identity.clientId
            privateKey = Base64URL.encode(identity.privateKey.rawRepresentation)
            self.name = name
        }

        var isValid: Bool { PairingLink.isLowercaseUUID(clientId) && key != nil }

        private var key: Curve25519.Signing.PrivateKey? {
            Base64URL.decode(privateKey).flatMap { try? Curve25519.Signing.PrivateKey(rawRepresentation: $0) }
        }

        var identity: ClientIdentity {
            ClientIdentity(clientId: clientId, privateKey: key!)
        }
    }

    /// The item of one console.
    private struct ConsoleRecord: Codable {
        var device: PairedDevice
        /// The certificate pin (`k` of the pairing link).
        let pin: String
    }
}
