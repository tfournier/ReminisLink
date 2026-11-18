import CryptoKit
import Foundation

/// Pairing method of `POST /api/v1/pair`.
public enum PairingMethod: String, Codable, Sendable {
    case qr
    case pin
}

/// What the user gave the app: the secret of the QR code or the PIN.
public enum PairingSecret: Sendable, Equatable {
    /// The 32 bytes carried by the QR code (`s=` parameter, base64url).
    case qr(Data)
    /// The 6-digit fallback PIN.
    case pin(String)

    public var method: PairingMethod {
        switch self {
        case .qr: .qr
        case .pin: .pin
        }
    }

    /// Input keying material: the secret bytes, or the UTF-8 bytes of the PIN.
    public var ikm: Data {
        switch self {
        case .qr(let secret): secret
        case .pin(let pin): Data(pin.utf8)
        }
    }
}

/// Body of `POST /api/v1/pair`.
public struct PairRequest: Codable, Sendable, Equatable {
    public let method: PairingMethod
    public let clientId: String
    public let clientName: String
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
    /// The device answered for another device id.
    case deviceMismatch
    /// `deviceNonce` is not 32 bytes of base64url.
    case invalidDeviceNonce
    /// The device proof does not match: the key is discarded.
    case invalidDeviceProof
}

/// Pairing primitives (docs/specs/03-protocole.md §3.2).
public enum Pairing {
    /// Length of clientNonce and deviceNonce.
    public static let nonceSize = 32
    /// Length of the shared key.
    public static let keySize = 32

    public static func clientProofMessage(deviceId: String, clientId: String, clientNonce: String) -> String {
        "RL1-PAIR-CLIENT|" + deviceId + "|" + clientId + "|" + clientNonce
    }

    public static func deviceProofMessage(deviceId: String, clientId: String) -> String {
        "RL1-PAIR-DEVICE|" + deviceId + "|" + clientId
    }

    public static func keyInfo(deviceId: String, clientId: String) -> String {
        "RL1-KEY|" + deviceId + "|" + clientId
    }

    /// A new client id: a lowercase UUID (the pak refuses uppercase).
    public static func makeClientId() -> String {
        UUID().uuidString.lowercased()
    }

    /// 32 random bytes.
    public static func makeNonce() -> Data {
        SymmetricKey(size: .init(bitCount: nonceSize * 8)).withUnsafeBytes { Data($0) }
    }

    /// b64u(HMAC-SHA256(ikm, "RL1-PAIR-CLIENT|deviceId|clientId|clientNonce")),
    /// with clientNonce as the base64url string that is sent.
    public static func clientProof(ikm: Data, deviceId: String, clientId: String, clientNonce: String) -> String {
        let message = clientProofMessage(deviceId: deviceId, clientId: clientId, clientNonce: clientNonce)
        return Base64URL.encode(Data(HMAC<SHA256>.authenticationCode(for: Data(message.utf8), using: SymmetricKey(data: ikm))))
    }

    /// HKDF-SHA256(ikm, salt = clientNonce || deviceNonce,
    /// info = "RL1-KEY|deviceId|clientId", L = 32).
    public static func deriveKey(
        ikm: Data, clientNonce: Data, deviceNonce: Data, deviceId: String, clientId: String
    ) -> SymmetricKey {
        HKDF<SHA256>.deriveKey(
            inputKeyMaterial: SymmetricKey(data: ikm),
            salt: clientNonce + deviceNonce,
            info: Data(keyInfo(deviceId: deviceId, clientId: clientId).utf8),
            outputByteCount: keySize
        )
    }

    /// b64u(HMAC-SHA256(key, "RL1-PAIR-DEVICE|deviceId|clientId")).
    public static func deviceProof(key: SymmetricKey, deviceId: String, clientId: String) -> String {
        let message = deviceProofMessage(deviceId: deviceId, clientId: clientId)
        return Base64URL.encode(Data(HMAC<SHA256>.authenticationCode(for: Data(message.utf8), using: key)))
    }

    /// Checks the device proof in constant time.
    public static func isValidDeviceProof(_ proof: String, key: SymmetricKey, deviceId: String, clientId: String) -> Bool {
        guard let mac = Base64URL.decode(proof) else { return false }
        let message = deviceProofMessage(deviceId: deviceId, clientId: clientId)
        return HMAC<SHA256>.isValidAuthenticationCode(mac, authenticating: Data(message.utf8), using: key)
    }
}

/// One pairing exchange, from the request to the verified key.
public struct PairingAttempt: Sendable {
    public let secret: PairingSecret
    public let deviceId: String
    public let clientId: String
    public let clientName: String
    public let clientNonce: Data

    public init(
        secret: PairingSecret,
        deviceId: String,
        clientId: String,
        clientName: String,
        clientNonce: Data = Pairing.makeNonce()
    ) {
        self.secret = secret
        self.deviceId = deviceId
        self.clientId = clientId
        self.clientName = clientName
        self.clientNonce = clientNonce
    }

    /// The body to send to `POST /api/v1/pair`.
    public var request: PairRequest {
        let nonce = Base64URL.encode(clientNonce)
        return PairRequest(
            method: secret.method,
            clientId: clientId,
            clientName: clientName,
            clientNonce: nonce,
            proof: Pairing.clientProof(ikm: secret.ikm, deviceId: deviceId, clientId: clientId, clientNonce: nonce)
        )
    }

    /// Derives the key from the response and checks the device proof
    /// (mutual authentication). Store the key in the Keychain only on success.
    public func finish(_ response: PairResponse) throws -> SymmetricKey {
        guard response.deviceId == deviceId else { throw PairingError.deviceMismatch }
        guard let deviceNonce = Base64URL.decode(response.deviceNonce), deviceNonce.count == Pairing.nonceSize else {
            throw PairingError.invalidDeviceNonce
        }
        let key = Pairing.deriveKey(
            ikm: secret.ikm, clientNonce: clientNonce, deviceNonce: deviceNonce,
            deviceId: deviceId, clientId: clientId
        )
        guard Pairing.isValidDeviceProof(response.proof, key: key, deviceId: deviceId, clientId: clientId) else {
            throw PairingError.invalidDeviceProof
        }
        return key
    }
}
