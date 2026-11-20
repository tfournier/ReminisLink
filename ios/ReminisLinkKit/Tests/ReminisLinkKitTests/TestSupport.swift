import CryptoKit
import Foundation
@testable import ReminisLinkKit

extension DeviceCredentials {
    /// Credentials of a fresh identity no pak knows (unless registered).
    static func random(deviceId: String = "5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0") -> DeviceCredentials {
        DeviceCredentials(
            deviceId: deviceId, identity: .generate(), pin: Base64URL.encode(Data(repeating: 0xAB, count: 32))
        )
    }
}
