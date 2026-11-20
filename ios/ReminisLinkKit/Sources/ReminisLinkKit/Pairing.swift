import CryptoKit
import Foundation

/// Body of `POST /api/v1/pair` (pairing v2, M13).
public struct PairRequest: Codable, Sendable, Equatable {
    public let clientId: String
    public let clientName: String
    public let platform: ClientPlatform
    /// The Ed25519 public key (32 raw bytes), base64url.
    public let clientPublicKey: String
    public let clientNonce: String
    public let proof: String
}

/// Response of `POST /api/v1/pair`.
public struct PairResponse: Codable, Sendable, Equatable {
    public let deviceId: String
    public let deviceName: String
    public let deviceNonce: String
    public let proof: String

    public init(deviceId: String, deviceName: String, deviceNonce: String, proof: String) {
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.deviceNonce = deviceNonce
        self.proof = proof
    }
}

public enum PairingError: Error, Equatable, Sendable {
    /// The QR secret is not 32 bytes.
    case invalidSecret
    /// clientNonce is not 32 bytes.
    case invalidNonce
    /// The device answered for another device id.
    case deviceMismatch
    /// `deviceNonce` is not 32 bytes of base64url.
    case invalidDeviceNonce
    /// The device proof does not match: nothing is stored.
    case invalidDeviceProof
}

/// Pairing primitives (docs/specs/03-protocole.md §3.2, pairing v2). No
/// shared key is derived: the secret of the QR code only proves, both
/// ways, that each side read the console's screen.
public enum Pairing {
    /// Length of the QR secret.
    public static let secretSize = 32
    /// Length of clientNonce and deviceNonce.
    public static let nonceSize = 32

    public static func clientProofMessage(
        deviceId: String, clientId: String, clientPublicKey: String, clientNonce: String
    ) -> String {
        ["RL2-PAIR-CLIENT", deviceId, clientId, clientPublicKey, clientNonce].joined(separator: "|")
    }

    public static func deviceProofMessage(
        deviceId: String, clientId: String, clientPublicKey: String, clientNonce: String, deviceNonce: String
    ) -> String {
        ["RL2-PAIR-DEVICE", deviceId, clientId, clientPublicKey, clientNonce, deviceNonce].joined(separator: "|")
    }

    /// Longest `clientName`, in Unicode scalars (the pak counts runes).
    public static let maxClientNameScalars = 64

    /// A `clientName` for `POST /pair`: trimmed, at most 64 Unicode scalars
    /// without cutting a character in two, `fallback` when empty.
    public static func clientName(from name: String, fallback: String = "iPhone") -> String {
        var result = ""
        var count = 0
        for character in name.trimmingCharacters(in: .whitespacesAndNewlines) {
            let scalars = character.unicodeScalars.count
            if count + scalars > maxClientNameScalars { break }
            result.append(character)
            count += scalars
        }
        result = result.trimmingCharacters(in: .whitespacesAndNewlines)
        return result.isEmpty ? fallback : result
    }

    /// A new client id: a lowercase UUID (the pak refuses uppercase).
    public static func makeClientId() -> String {
        UUID().uuidString.lowercased()
    }

    /// 32 random bytes.
    public static func makeNonce() -> Data {
        SymmetricKey(size: .init(bitCount: nonceSize * 8)).withUnsafeBytes { Data($0) }
    }

    /// b64u(HMAC-SHA256(secret, message)).
    public static func proof(secret: Data, message: String) -> String {
        Base64URL.encode(Data(HMAC<SHA256>.authenticationCode(for: Data(message.utf8), using: SymmetricKey(data: secret))))
    }

    /// Checks a proof in constant time.
    public static func isValidProof(_ proof: String, secret: Data, message: String) -> Bool {
        guard let mac = Base64URL.decode(proof) else { return false }
        return HMAC<SHA256>.isValidAuthenticationCode(mac, authenticating: Data(message.utf8), using: SymmetricKey(data: secret))
    }
}

/// One pairing exchange, from the request to the verified answer.
public struct PairingAttempt: Sendable {
    public let secret: Data
    public let deviceId: String
    public let identity: ClientIdentity
    public let clientName: String
    public let platform: ClientPlatform
    public let clientNonce: Data

    /// Throws ``PairingError/invalidSecret`` for a secret that is not 32
    /// bytes, ``PairingError/invalidNonce`` for a client nonce that is not
    /// 32 bytes.
    public init(
        secret: Data,
        deviceId: String,
        identity: ClientIdentity,
        clientName: String,
        platform: ClientPlatform = .apple,
        clientNonce: Data = Pairing.makeNonce()
    ) throws {
        guard secret.count == Pairing.secretSize else { throw PairingError.invalidSecret }
        guard clientNonce.count == Pairing.nonceSize else { throw PairingError.invalidNonce }
        self.secret = secret
        self.deviceId = deviceId
        self.identity = identity
        self.clientName = clientName
        self.platform = platform
        self.clientNonce = clientNonce
    }

    /// The body to send to `POST /api/v1/pair`.
    public var request: PairRequest {
        let nonce = Base64URL.encode(clientNonce)
        let publicKey = identity.publicKey
        let message = Pairing.clientProofMessage(
            deviceId: deviceId, clientId: identity.clientId, clientPublicKey: publicKey, clientNonce: nonce
        )
        return PairRequest(
            clientId: identity.clientId,
            clientName: clientName,
            platform: platform,
            clientPublicKey: publicKey,
            clientNonce: nonce,
            proof: Pairing.proof(secret: secret, message: message)
        )
    }

    /// Checks the device proof (mutual authentication: the console knew
    /// the secret). Store the pairing only on success.
    public func finish(_ response: PairResponse) throws {
        guard response.deviceId == deviceId else { throw PairingError.deviceMismatch }
        guard let deviceNonce = Base64URL.decode(response.deviceNonce), deviceNonce.count == Pairing.nonceSize else {
            throw PairingError.invalidDeviceNonce
        }
        let message = Pairing.deviceProofMessage(
            deviceId: deviceId, clientId: identity.clientId, clientPublicKey: identity.publicKey,
            clientNonce: Base64URL.encode(clientNonce), deviceNonce: response.deviceNonce
        )
        guard Pairing.isValidProof(response.proof, secret: secret, message: message) else {
            throw PairingError.invalidDeviceProof
        }
    }
}
