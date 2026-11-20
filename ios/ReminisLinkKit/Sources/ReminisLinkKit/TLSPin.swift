import CryptoKit
import Foundation
import Security

/// The certificate pin of a console (docs/specs/03-protocole.md §1, M13):
/// `b64u(SHA-256(DER of the SubjectPublicKeyInfo))` of its self-signed
/// ECDSA P-256 certificate. The app trusts that certificate only: no system
/// trust, no certificate authority, no host name check (the certificate is
/// not "standards compliant" for the system: 20-year validity, no name).
public enum TLSPin {
    /// Length of a pin once decoded (SHA-256).
    public static let size = 32

    /// The fixed DER header of a P-256 SubjectPublicKeyInfo, before the
    /// 65-byte uncompressed point: SEQUENCE { SEQUENCE { id-ecPublicKey,
    /// prime256v1 }, BIT STRING (0 unused bits) }.
    public static let p256SPKIHeader = Data([
        0x30, 0x59, 0x30, 0x13, 0x06, 0x07, 0x2a, 0x86, 0x48, 0xce, 0x3d, 0x02, 0x01, 0x06, 0x08, 0x2a,
        0x86, 0x48, 0xce, 0x3d, 0x03, 0x01, 0x07, 0x03, 0x42, 0x00,
    ])

    /// The SubjectPublicKeyInfo of a P-256 key from its X9.63 uncompressed
    /// point (`04 || X || Y`, what `SecKeyCopyExternalRepresentation`
    /// returns); nil for another shape.
    public static func spki(x963 point: Data) -> Data? {
        guard point.count == 65, point.first == 0x04 else { return nil }
        return p256SPKIHeader + point
    }

    /// The pin of a SubjectPublicKeyInfo.
    public static func pin(spki: Data) -> String {
        Base64URL.encode(Data(SHA256.hash(data: spki)))
    }

    /// The pin of a P-256 public key, nil for another kind of key.
    public static func pin(publicKey: SecKey) -> String? {
        guard let attributes = SecKeyCopyAttributes(publicKey) as? [String: Any],
              attributes[kSecAttrKeyType as String] as? String == kSecAttrKeyTypeECSECPrimeRandom as String,
              (attributes[kSecAttrKeySizeInBits as String] as? Int) == 256,
              let point = SecKeyCopyExternalRepresentation(publicKey, nil) as Data?,
              let spki = spki(x963: point)
        else { return nil }
        return pin(spki: spki)
    }

    /// The pin of a certificate, nil if its key is not a P-256 key.
    public static func pin(certificate: SecCertificate) -> String? {
        SecCertificateCopyKey(certificate).flatMap { pin(publicKey: $0) }
    }

    /// The pin of the leaf certificate a server presented. The trust object
    /// is never evaluated (no system trust).
    public static func pin(trust: SecTrust) -> String? {
        guard let chain = SecTrustCopyCertificateChain(trust) as? [SecCertificate], let leaf = chain.first else {
            return nil
        }
        return pin(certificate: leaf)
    }

    /// Whether the server presented the pinned certificate key. Compared in
    /// constant time, on the decoded bytes.
    public static func matches(trust: SecTrust, pin expected: String) -> Bool {
        guard let actual = pin(trust: trust) else { return false }
        return equal(actual, expected)
    }

    static func equal(_ a: String, _ b: String) -> Bool {
        guard let x = Base64URL.decode(a), let y = Base64URL.decode(b), x.count == size, y.count == size else {
            return false
        }
        var difference: UInt8 = 0
        for (p, q) in zip(x, y) { difference |= p ^ q }
        return difference == 0
    }
}

/// Answers the server trust challenges of the requests to one console with
/// its pin. Without a pin, every TLS server is refused. A refusal is
/// remembered, so the request fails with ``APIError/identityChanged``
/// instead of a plain cancellation.
public final class PinnedTrust: @unchecked Sendable {
    public let pin: String?
    private let lock = NSLock()
    private var _refused = false

    public init(pin: String?) {
        self.pin = pin
    }

    /// The server presented another certificate key than the pinned one.
    public var refused: Bool { lock.withLock { _refused } }

    /// The answer to an authentication challenge of a task. Only server
    /// trust is handled; any other challenge gets the default handling.
    public func evaluate(_ challenge: URLAuthenticationChallenge) -> (URLSession.AuthChallengeDisposition, URLCredential?) {
        guard challenge.protectionSpace.authenticationMethod == NSURLAuthenticationMethodServerTrust else {
            return (.performDefaultHandling, nil)
        }
        guard let trust = challenge.protectionSpace.serverTrust, let pin, TLSPin.matches(trust: trust, pin: pin) else {
            lock.withLock { _refused = true }
            return (.cancelAuthenticationChallenge, nil)
        }
        return (.useCredential, URLCredential(trust: trust))
    }
}
