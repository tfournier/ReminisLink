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

/// Generic password items of one service. Items are only readable after the
/// first unlock, never leave this device and are never synchronized with
/// iCloud (`kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly`, not
/// synchronizable).
///
/// Without an explicit access group, items go to the first group of the
/// `keychain-access-groups` entitlement: the group shared with the share
/// extension.
public struct KeychainStore: SecureStore {
    public let service: String
    public let accessGroup: String?

    public init(service: String, accessGroup: String? = nil) {
        self.service = service
        self.accessGroup = accessGroup
    }

    /// The query that identifies items of this store (one account, or all).
    func baseQuery(account: String?) -> [String: Any] {
        var query: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrSynchronizable as String: kCFBooleanFalse as Any,
        ]
        if let account { query[kSecAttrAccount as String] = account }
        if let accessGroup { query[kSecAttrAccessGroup as String] = accessGroup }
        return query
    }

    /// Attributes written with every item.
    func writeAttributes(data: Data) -> [String: Any] {
        [
            kSecValueData as String: data,
            kSecAttrAccessible as String: kSecAttrAccessibleAfterFirstUnlockThisDeviceOnly,
        ]
    }

    public func data(for account: String) throws -> Data? {
        var query = baseQuery(account: account)
        query[kSecReturnData as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitOne
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
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
        var status = SecItemUpdate(query as CFDictionary, attributes as CFDictionary)
        if status == errSecItemNotFound {
            status = SecItemAdd(query.merging(attributes) { $1 } as CFDictionary, nil)
        }
        guard status == errSecSuccess else { throw KeychainError.status(status) }
    }

    public func remove(account: String) throws {
        let status = SecItemDelete(baseQuery(account: account) as CFDictionary)
        guard status == errSecSuccess || status == errSecItemNotFound else { throw KeychainError.status(status) }
    }

    public func allAccounts() throws -> [String] {
        var query = baseQuery(account: nil)
        query[kSecReturnAttributes as String] = true
        query[kSecMatchLimit as String] = kSecMatchLimitAll
        var result: CFTypeRef?
        let status = SecItemCopyMatching(query as CFDictionary, &result)
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
