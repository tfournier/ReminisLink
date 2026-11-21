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
///   identities (`clientId` and Ed25519 private key, one item each, keyed
///   by `clientId`), a pointer to the current one (used for new pairings,
///   with the name shown on the consoles), and, for each console, its id,
///   the `clientId` it was paired with, its certificate pin, its last
///   validated address, names and model. The iPhone, iPad and Mac of one
///   Apple account share them: they pair once.
///
///   Each console signs with the identity it was paired with, never "the
///   current one": two devices that create an identity each before the
///   Keychain syncs end with two identity items and both pairings keep
///   working; the pointer settles on one of them for the next pairings.
///   Identity items are never deleted automatically (they are tiny): while
///   the iCloud Keychain syncs, a key may arrive before the console record
///   that refers to it, and deleting it as "unused" would break that
///   pairing on every device.
/// - `UserDefaults` of the app group: only the consoles paired before M13
///   (RL1), kept in the list as "needs re-pairing" until they are paired
///   again or forgotten. Their keys are deleted (``legacySecureStore``).
@MainActor
@Observable
public final class DeviceStore {
    /// Longest display name.
    public static let maxNameLength = 64
    static let defaultsKey = "pairedDevices"
    /// The pointer to the current identity (and the pairing name).
    static let identityAccount = "identity"
    static let identityPrefix = "identity."
    static let consolePrefix = "console."
    static func account(deviceId: String) -> String { consolePrefix + deviceId }
    static func account(clientId: String) -> String { identityPrefix + clientId }

    public private(set) var devices: [PairedDevice] = []
    /// Called after this device wrote or removed a shared item (identity,
    /// pointer, console): M15, the app signals the other devices.
    @ObservationIgnored public var onSharedItemsChange: (@MainActor () -> Void)?

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

    /// The current identity of this app, for new pairings, shared through
    /// the iCloud Keychain.
    ///
    /// Convergence (M15, a Mac or an iPad that opens the app before the
    /// iCloud Keychain delivered everything):
    /// - the identity the pointer names, when its item is here;
    /// - else (no pointer, or a pointer whose identity item is still on its
    ///   way) an identity item already here: the one most consoles were
    ///   paired with, then the smallest `clientId` (every device picks the
    ///   same);
    /// - else a new identity.
    ///
    /// The pointer is written only when there is none: a pointer whose
    /// identity has not arrived yet is never replaced, since the other
    /// devices would then follow an identity they may not have yet, and
    /// fork again.
    public func identity() throws -> ClientIdentity {
        let pointer = try currentPointer()
        if let pointer, let identity = try identity(clientId: pointer.clientId) { return identity }
        let identity: ClientIdentity
        if let stored = try bestStoredIdentity() {
            identity = stored
        } else {
            identity = ClientIdentity.generate()
            try secureStore.set(
                try JSONEncoder().encode(IdentityRecord(identity: identity)), for: Self.account(clientId: identity.clientId)
            )
            sharedItemsChanged()
        }
        if pointer == nil { try writePointer(Pointer(clientId: identity.clientId, name: nil)) }
        return identity
    }

    /// The name of the identity shown on the consoles (`clientName`), as
    /// last typed; nil before. Shared like the identity.
    public func pairingName() -> String? {
        guard let pointer = try? currentPointer() else { return nil }
        return pointer.name
    }

    /// Records the name typed for the identity (sent at the next pairing).
    /// Only the name changes: the pointer keeps naming its identity, even
    /// one whose item has not arrived yet.
    public func setPairingName(_ name: String) throws {
        let clientId = try currentPointer()?.clientId ?? identity().clientId
        try writePointer(Pointer(clientId: clientId, name: Self.clean(name)))
    }

    /// The best identity item present (see ``identity()``), nil when there
    /// is none. Unreadable items are skipped.
    private func bestStoredIdentity() throws -> ClientIdentity? {
        let accounts = try secureStore.allAccounts()
        var pairings: [String: Int] = [:]
        for account in accounts where account.hasPrefix(Self.consolePrefix) {
            let id = String(account.dropFirst(Self.consolePrefix.count))
            if let record = try? consoleRecord(id) { pairings[record.clientId, default: 0] += 1 }
        }
        let identities = accounts.filter { $0.hasPrefix(Self.identityPrefix) }
            .compactMap { try? identity(clientId: String($0.dropFirst(Self.identityPrefix.count))) }
        return identities.min { a, b in
            let countA = pairings[a.clientId, default: 0]
            let countB = pairings[b.clientId, default: 0]
            return countA != countB ? countA > countB : a.clientId < b.clientId
        }
    }

    /// The identity with this `clientId`, nil if its item is absent.
    func identity(clientId: String) throws -> ClientIdentity? {
        guard let data = try secureStore.data(for: Self.account(clientId: clientId)) else { return nil }
        guard let record = try? JSONDecoder().decode(IdentityRecord.self, from: data), record.isValid,
              record.clientId == clientId
        else { throw KeychainError.unexpectedData }
        return record.identity
    }

    private func currentPointer() throws -> Pointer? {
        guard let data = try secureStore.data(for: Self.identityAccount) else { return nil }
        guard let pointer = try? JSONDecoder().decode(Pointer.self, from: data),
              PairingLink.isLowercaseUUID(pointer.clientId)
        else { throw KeychainError.unexpectedData }
        return pointer
    }

    private func writePointer(_ pointer: Pointer) throws {
        try secureStore.set(try JSONEncoder().encode(pointer), for: Self.identityAccount)
        sharedItemsChanged()
    }

    /// Tells the other devices of the Apple account that the shared items
    /// changed (M15: the app sets a marker in the iCloud key-value store,
    /// whose change notification makes them read the Keychain again).
    private func sharedItemsChanged() {
        onSharedItemsChange?()
    }

    // MARK: - Consoles

    /// Stores a new pairing, or replaces the pairing of a console paired
    /// again. `clientId`: the identity `POST /pair` was sent with (default:
    /// the current one); `endpoint`: the address the pairing reached,
    /// authenticated by the pin.
    @discardableResult
    public func save(
        deviceId: String, name: String, model: DeviceModel, clientId: String? = nil, pin: String,
        endpoint: Endpoint? = nil, consoleName: String? = nil, now: Date = Date()
    ) throws -> PairedDevice {
        let clientId = try clientId ?? identity().clientId
        guard try identity(clientId: clientId) != nil else { throw KeychainError.unexpectedData }
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
        try write(ConsoleRecord(device: device, clientId: clientId, pin: pin, endpoint: endpoint))
        legacy.removeAll { $0.id == deviceId }
        persistLegacy()
        reload()
        return device
    }

    /// The credentials of a device: the identity it was paired with and
    /// its pin. Nil if the console is not paired (never paired, or paired
    /// before M13), or if its identity item has not arrived yet.
    public func credentials(for deviceId: String) throws -> DeviceCredentials? {
        guard let record = try consoleRecord(deviceId) else { return nil }
        guard let identity = try identity(clientId: record.clientId) else { return nil }
        return DeviceCredentials(deviceId: deviceId, identity: identity, pin: record.pin)
    }

    /// What this device can do with a console's pairing.
    public enum PairingState: Equatable, Sendable {
        /// Not paired with an identity (never paired, forgotten, or paired
        /// before M13).
        case notPaired
        /// Paired (by another device of the Apple account, or by this one),
        /// but the identity the console knows has not reached this device
        /// through the iCloud Keychain yet: nothing to sign with, and
        /// nothing to pair again (the pairing works on the other devices).
        case awaitingIdentity
        case paired
    }

    /// The state of a console's pairing on this device (see ``PairingState``).
    public func pairingState(for deviceId: String) throws -> PairingState {
        guard let record = try consoleRecord(deviceId) else { return .notPaired }
        return try identity(clientId: record.clientId) == nil ? .awaitingIdentity : .paired
    }

    /// The last address where the console presented its pinned certificate
    /// (at pairing or a later connection), a fallback when Bonjour does not
    /// find it.
    public func lastEndpoint(for deviceId: String) -> Endpoint? {
        (try? consoleRecord(deviceId))?.endpoint
    }

    /// Records the address of a working connection (pinned TLS, signed
    /// requests accepted).
    public func noteEndpoint(_ endpoint: Endpoint, for deviceId: String) {
        guard var record = try? consoleRecord(deviceId), record.endpoint != endpoint else { return }
        record.endpoint = endpoint
        try? write(record)
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
        sharedItemsChanged()
        legacy.removeAll { $0.id == deviceId }
        persistLegacy()
        reload()
    }

    /// Forgets the "needs re-pairing" entry of a console paired before M13
    /// (this device's `UserDefaults` only). Never touches the synchronized
    /// Keychain: an M13 pairing of the same console that arrived meanwhile
    /// from another device stays.
    public func forgetRepairMarker(_ deviceId: String) {
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
              PairingLink.isLowercaseUUID(record.clientId), Base64URL.decode(record.pin)?.count == TLSPin.size
        else { throw KeychainError.unexpectedData }
        return record
    }

    private func write(_ record: ConsoleRecord) throws {
        try secureStore.set(try JSONEncoder().encode(record), for: Self.account(deviceId: record.device.id))
        sharedItemsChanged()
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

    /// The item of one identity (`identity.<clientId>`).
    private struct IdentityRecord: Codable {
        let clientId: String
        /// The 32-byte Ed25519 private key (raw representation), base64url.
        let privateKey: String

        init(identity: ClientIdentity) {
            clientId = identity.clientId
            privateKey = Base64URL.encode(identity.privateKey.rawRepresentation)
        }

        var isValid: Bool { PairingLink.isLowercaseUUID(clientId) && key != nil }

        private var key: Curve25519.Signing.PrivateKey? {
            Base64URL.decode(privateKey).flatMap { try? Curve25519.Signing.PrivateKey(rawRepresentation: $0) }
        }

        var identity: ClientIdentity {
            ClientIdentity(clientId: clientId, privateKey: key!)
        }
    }

    /// The pointer to the current identity (`identity`).
    private struct Pointer: Codable {
        let clientId: String
        /// The name shown on the consoles, as typed.
        let name: String?
    }

    /// The item of one console.
    private struct ConsoleRecord: Codable {
        var device: PairedDevice
        /// The identity the console was paired with: it signs its requests.
        let clientId: String
        /// The certificate pin (`k` of the pairing link).
        let pin: String
        /// The last address where the console presented that certificate.
        var endpoint: Endpoint?
    }
}
