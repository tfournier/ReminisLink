import Foundation
import ReminisLinkDemo
import ReminisLinkKit
@testable import ReminisLink

extension AppModel {
    /// Pairs with the fake pak as the pairing flow does (pairing v2, the
    /// identity of the store) and stores the pairing with the pak's pin.
    func pairDirectly(with pak: FakePak, name: String = "Brick", clientName: String = "Test") async throws {
        let identity = try store.identity()
        let attempt = try PairingAttempt(secret: pak.secret, deviceId: pak.deviceId, identity: identity, clientName: clientName)
        try await APIClient(endpoint: pak.endpoint, pin: pak.pin, session: pak.makeSession()).pair(attempt)
        try store.save(deviceId: pak.deviceId, name: name, model: .brick, pin: pak.pin)
    }
}
