import Foundation
import Security

/// Small secrets storage: the Keychain in the app, memory in tests and in
/// the demo mode.
public protocol SecureStore: Sendable {
    func data(for account: String) throws -> Data?
    func set(_ data: Data, for account: String) throws
    func remove(account: String) throws
    func allAccounts() throws -> [String]
}

public enum KeychainError: Error, Equatable, Sendable {
    case status(OSStatus)
    case unexpectedData
}

/// The `SecItem` functions, injectable so tests can check the queries.
public struct KeychainAPI: Sendable {
    public var copyMatching: @Sendable (_ query: [String: Any], _ result: UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus
    public var add: @Sendable (_ attributes: [String: Any]) -> OSStatus
    public var update: @Sendable (_ query: [String: Any], _ attributes: [String: Any]) -> OSStatus
    public var delete: @Sendable (_ query: [String: Any]) -> OSStatus

    public init(
        copyMatching: @escaping @Sendable ([String: Any], UnsafeMutablePointer<CFTypeRef?>?) -> OSStatus,
        add: @escaping @Sendable ([String: Any]) -> OSStatus,
        update: @escaping @Sendable ([String: Any], [String: Any]) -> OSStatus,
        delete: @escaping @Sendable ([String: Any]) -> OSStatus
    ) {
        self.copyMatching = copyMatching
        self.add = add
        self.update = update
        self.delete = delete
    }

    /// The real Keychain.
    public static let system = KeychainAPI(
        copyMatching: { SecItemCopyMatching($0 as CFDictionary, $1) },
        add: { SecItemAdd($0 as CFDictionary, nil) },
        update: { SecItemUpdate($0 as CFDictionary, $1 as CFDictionary) },
        delete: { SecItemDelete($0 as CFDictionary) }
    )
}

/// Generic password items of one service, readable after the first unlock.
///
/// - `synchronizable` (M13): the items go to the iCloud Keychain
///   (`kSecAttrSynchronizable`, `kSecAttrAccessibleAfterFirstUnlock`), so
///   the iPhone, iPad and Mac of one Apple account share them: the client
///   identity and the consoles paired with it.
/// - Otherwise they never leave this device
///   (`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`): the pairings of
///   RL1, only read to be deleted.
///
/// Without an explicit access group, items go to the first group of the
/// `keychain-access-groups` entitlement: the group shared with the share
/// extension.
public struct KeychainStore: SecureStore {
    public let service: String
    public let accessGroup: String?
    public let synchronizable: Bool
    private let api: KeychainAPI

    public init(service: String, accessGroup: String? = nil, synchronizable: Bool = false, api: KeychainAPI = .system) {
        self.service = service
        self.accessGroup = accessGroup
        self.synchronizable = synchronizable
        self.api = api
    }

    /// The query that identifies items of this store (one account, or all).
    func baseQuery(account: String?) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrSynchronizable as String: (synchronizable ? kCFBooleanTrue : kCFBooleanFalse) as Any,
        ]
        if let account { query[kSecAttrAccount as String] = account }
        if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
        return query
    }

    /// Attributes written with every item.
    func writeAttributes(data: Data) -> [String: Any] {
        [
            kSecValueData as String: data,
            kSecAttrAccessible as String: synchronizable
                ? kSecAttrAccessibleAfterFirstUnlock : kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
    }

    public func data(for account: String) throws -> Data? {
        var query = baseQuery(account: account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = api.copyMatching(query, &result)
        switch status {
        case errSecSuccess:
            guard let data = result as? Data else { throw KeychainError.unexpectedData }
            return data
        case errSecItemNotFound:
            return nil
        default:
            throw KeychainError.status(status)
        }
    }

    public func set(_ data: Data, for account: String) throws {
        let query = baseQuery(account: account)
        let attributes = writeAttributes(data: data)
        var status = api.update(query, attributes)
        if status == errSecItemNotFound {
            status = api.add(query.merging(attributes) { $1 })
        }
        guard status == errSecSuccess else { throw KeychainError.status(status) }
    }

    public func remove(account: String) throws {
        let status = api.delete(baseQuery(account: account))
        guard status == errSecSuccess || status == errSecItemNotFound else { throw KeychainError.status(status) }
    }

    public func allAccounts() throws -> [String] {
        var query = baseQuery(account: nil)
        query[kSecReturnAttributes as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitAll
        var result: CFTypeRef?
        let status = api.copyMatching(query, &result)
        switch status {
        case errSecSuccess:
            let items = result as? [[String: Any]] ?? []
            return items.compactMap { $0[kSecAttrAccount as String] as? String }.sorted()
        case errSecItemNotFound:
            return []
        default:
            throw KeychainError.status(status)
        }
    }
}

/// In-memory ``SecureStore``.
public final class InMemorySecureStore: SecureStore, @unchecked Sendable {
    private let lock = NSLock()
    private var items: [String: Data] = [:]

    public init() {}

    public func data(for account: String) throws -> Data? {
        lock.withLock { items[account] }
    }

    public func set(_ data: Data, for account: String) throws {
        lock.withLock { items[account] = data }
    }

    public func remove(account: String) throws {
        _ = lock.withLock { items.removeValue(forKey: account) }
    }

    public func allAccounts() throws -> [String] {
        lock.withLock { items.keys.sorted() }
    }
}
