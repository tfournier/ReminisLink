import Foundation

/// The iCloud key-value store, injectable in tests.
public protocol KeyValueStoring: AnyObject {
    func object(forKey key: String) -> Any?
    func set(_ value: Any?, forKey key: String)
    @discardableResult func synchronize() -> Bool
}

extension NSUbiquitousKeyValueStore: KeyValueStoring {}

/// "The pairings changed" between the devices of one Apple account (M15).
///
/// The iCloud Keychain, which carries the identity and the consoles, sends
/// no change notification: a Mac left open would not see a console paired
/// on the iPhone. So every device that writes a shared pairing item also
/// sets a marker in the iCloud key-value store, whose
/// `didChangeExternallyNotification` reaches the other devices; they read
/// the Keychain again. The marker holds nothing about the user, the
/// devices or the consoles: a counter and a random token (so two devices
/// that reach the same count still change the value), no date.
///
/// The marker may arrive before the Keychain items it announces: the app
/// reads the Keychain again a little later too, and on every return to the
/// foreground.
@MainActor
public final class PairingChangeSignal {
    public static let key = "pairingsChanged"

    private let store: KeyValueStoring
    private let center: NotificationCenter
    private let onExternalChange: @MainActor () -> Void
    private var observer: NSObjectProtocol?

    public init(
        store: KeyValueStoring = NSUbiquitousKeyValueStore.default, center: NotificationCenter = .default,
        onExternalChange: @escaping @MainActor () -> Void
    ) {
        self.store = store
        self.center = center
        self.onExternalChange = onExternalChange
    }

    /// Observes the changes made by the other devices and asks iCloud for
    /// the latest values (at launch).
    public func start() {
        guard observer == nil else { return }
        observer = center.addObserver(
            forName: NSUbiquitousKeyValueStore.didChangeExternallyNotification, object: nil, queue: .main
        ) { [weak self] notification in
            let keys = notification.userInfo?[NSUbiquitousKeyValueStoreChangedKeysKey] as? [String]
            MainActor.assumeIsolated { self?.externalChange(keys: keys) }
        }
        store.synchronize()
    }

    public func stop() {
        if let observer { center.removeObserver(observer) }
        observer = nil
    }

    /// This device changed a shared pairing item (paired, forgot, renamed,
    /// created or chose an identity).
    public func noteLocalChange(token: UUID = UUID()) {
        let count = (current?["count"] as? Int ?? 0) + 1
        store.set(["count": count, "token": token.uuidString], forKey: Self.key)
        store.synchronize()
    }

    /// The marker as last read (nil before the first change).
    public var current: [String: Any]? {
        store.object(forKey: Self.key) as? [String: Any]
    }

    /// A change from another device: the Keychain is read again. Without
    /// the list of keys (initial sync, account change), read it anyway.
    func externalChange(keys: [String]?) {
        guard keys?.contains(Self.key) ?? true else { return }
        onExternalChange()
    }
}
