import CryptoKit
import Foundation

/// The identity of the app towards every console (docs/specs/03-protocole.md
/// §3.2, M13): a `clientId` and an Ed25519 key pair. The private key never
/// leaves the Keychain item it is stored in (a synchronized item: the
/// iPhone, iPad and Mac of one Apple account share it and pair once); the
/// console only keeps the public key.
public struct ClientIdentity: Sendable {
    public let clientId: String
    public let privateKey: Curve25519.Signing.PrivateKey

    public init(clientId: String, privateKey: Curve25519.Signing.PrivateKey) {
        self.clientId = clientId
        self.privateKey = privateKey
    }

    /// A new identity: a lowercase UUID and a fresh key pair.
    public static func generate() -> ClientIdentity {
        ClientIdentity(clientId: Pairing.makeClientId(), privateKey: Curve25519.Signing.PrivateKey())
    }

    /// The 32 raw bytes of the public key, base64url (`clientPublicKey`).
    public var publicKey: String {
        Base64URL.encode(privateKey.publicKey.rawRepresentation)
    }
}

extension ClientIdentity: Equatable {
    public static func == (a: ClientIdentity, b: ClientIdentity) -> Bool {
        a.clientId == b.clientId && a.privateKey.rawRepresentation == b.privateKey.rawRepresentation
    }
}

/// `platform` of `POST /pair`: the kind of client.
public enum ClientPlatform: String, Codable, Sendable {
    /// An identity shared by the Apple devices of one account.
    case apple
    case android
    case web
    case other
}

/// `X-RL-Platform` (informative, not signed): the kind of device sending a
/// request, shown on the console's Devices screen. Never used for any
/// decision.
public enum RequestPlatform: String, Sendable, CaseIterable {
    case iphone
    case ipad
    case mac

    public static let header = "X-RL-Platform"
}
