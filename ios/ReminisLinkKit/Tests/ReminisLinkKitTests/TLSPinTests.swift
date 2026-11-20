import CryptoKit
import Foundation
import Security
import Testing
@testable import ReminisLinkKit

/// The certificate pinning of M13 (docs/specs/03-protocole.md §1), on the
/// certificate of tls.json. The handshake itself is covered against the
/// real pak (ios/README.md, headless tests): a URLProtocol fake serves no
/// TLS.
@Suite("Certificate pinning")
struct TLSPinTests {
    func vectors() throws -> TLSVectors { try Vectors.load("tls", as: TLSVectors.self) }

    /// A trust object as a TLS handshake gives it (never evaluated).
    func trust() throws -> SecTrust {
        let certificate = try #require(VectorTests.certificate(pem: try vectors().certificatePem))
        var trust: SecTrust?
        let status = SecTrustCreateWithCertificates(certificate, SecPolicyCreateSSL(true, nil), &trust)
        #expect(status == errSecSuccess)
        return try #require(trust)
    }

    @Test func theLeafKeyIsPinned() throws {
        let trust = try trust()
        #expect(TLSPin.pin(trust: trust) == (try vectors().pin))
        #expect(TLSPin.matches(trust: trust, pin: try vectors().pin))
    }

    @Test func anotherKeyIsRefused() throws {
        let trust = try trust()
        let other = TLSPin.pin(spki: P256.Signing.PrivateKey().publicKey.derRepresentation)
        #expect(!TLSPin.matches(trust: trust, pin: other))
        // Malformed pins never match (not 32 bytes, not base64url).
        let pin = try vectors().pin
        for bad in ["", pin + "A", String(pin.dropLast()), (try vectors().pinStdBase64)] {
            #expect(!TLSPin.matches(trust: trust, pin: bad), "\(bad)")
        }
    }

    /// The system trust is never consulted: the self-signed certificate is
    /// accepted by its pin although the system would refuse it.
    @Test func systemTrustWouldRefuseThisCertificate() throws {
        let trust = try trust()
        var error: CFError?
        #expect(!SecTrustEvaluateWithError(trust, &error))
        #expect(TLSPin.matches(trust: trust, pin: try vectors().pin))
    }

    @Test func onlyP256KeysHaveAPin() throws {
        let attributes: [String: Any] = [kSecAttrKeyType as String: kSecAttrKeyTypeRSA, kSecAttrKeySizeInBits as String: 2048]
        var error: Unmanaged<CFError>?
        let rsa = try #require(SecKeyCreateRandomKey(attributes as CFDictionary, &error))
        #expect(TLSPin.pin(publicKey: try #require(SecKeyCopyPublicKey(rsa))) == nil)
        #expect(TLSPin.spki(x963: Data(repeating: 4, count: 64)) == nil)
        #expect(TLSPin.spki(x963: Data([0x05]) + Data(repeating: 1, count: 64)) == nil)
    }

    @Test func challengesWithoutTheServerTrustAreRefused() throws {
        let pinned = PinnedTrust(pin: try vectors().pin)
        // A server trust challenge without a trust object.
        let space = URLProtectionSpace(
            host: "192.168.1.42", port: 47821, protocol: NSURLProtectionSpaceHTTPS, realm: nil,
            authenticationMethod: NSURLAuthenticationMethodServerTrust
        )
        let (disposition, credential) = pinned.evaluate(Self.challenge(space))
        #expect(disposition == .cancelAuthenticationChallenge)
        #expect(credential == nil)
        #expect(pinned.refused)
    }

    @Test func otherChallengesGetTheDefaultHandling() {
        let pinned = PinnedTrust(pin: nil)
        let space = URLProtectionSpace(
            host: "192.168.1.42", port: 47821, protocol: NSURLProtectionSpaceHTTPS, realm: "x",
            authenticationMethod: NSURLAuthenticationMethodHTTPBasic
        )
        #expect(pinned.evaluate(Self.challenge(space)).0 == .performDefaultHandling)
        #expect(!pinned.refused)
    }

    /// A refused certificate makes the request fail with
    /// ``APIError/identityChanged``, not a plain cancellation.
    @Test func aRefusalIsReportedAsAnIdentityChange() {
        let trust = PinnedTrust(pin: nil)
        #expect(PakTaskDelegate.failure(URLError(.cancelled), trust: trust) is CancellationError)
        let space = URLProtectionSpace(
            host: "h", port: 1, protocol: NSURLProtectionSpaceHTTPS, realm: nil,
            authenticationMethod: NSURLAuthenticationMethodServerTrust
        )
        _ = trust.evaluate(Self.challenge(space))
        #expect(PakTaskDelegate.failure(URLError(.cancelled), trust: trust) as? APIError == .identityChanged)
        #expect(PakTaskDelegate.failure(URLError(.secureConnectionFailed), trust: trust) as? APIError == .identityChanged)
    }

    static func challenge(_ space: URLProtectionSpace) -> URLAuthenticationChallenge {
        URLAuthenticationChallenge(
            protectionSpace: space, proposedCredential: nil, previousFailureCount: 0, failureResponse: nil, error: nil,
            sender: NoSender()
        )
    }
}

private final class NoSender: NSObject, URLAuthenticationChallengeSender {
    func use(_ credential: URLCredential, for challenge: URLAuthenticationChallenge) {}
    func continueWithoutCredential(for challenge: URLAuthenticationChallenge) {}
    func cancel(_ challenge: URLAuthenticationChallenge) {}
}

/// The TLS handshake against a real pak (`make headless`), skipped unless
/// `RL_TEST_PAK_LINK` holds its pairing link (only `h`, `p`, `k` and `id`
/// are used; nothing is paired):
///
///     RL_TEST_PAK_LINK='reminislink://pair?v=2&...' swift test --filter PinnedHandshake
@Suite("Pinned handshake against a real pak")
struct PinnedHandshakeTests {
    static let link: PairingLink? = ProcessInfo.processInfo.environment["RL_TEST_PAK_LINK"]
        .flatMap { try? PairingLink.parse($0) }

    @Test(.enabled(if: link?.endpoint != nil))
    func onlyThePinnedCertificateIsAccepted() async throws {
        let link = try #require(Self.link)
        let endpoint = try #require(link.endpoint)
        let hello = try await APIClient(endpoint: endpoint, expectedDeviceId: link.deviceId, pin: link.pin).hello()
        #expect(hello.apiVersion == 2)
        let other = Base64URL.encode(Data(repeating: 1, count: 32))
        await #expect(throws: APIError.identityChanged) {
            try await APIClient(endpoint: endpoint, pin: other).hello()
        }
        await #expect(throws: APIError.identityChanged) {
            try await APIClient(endpoint: endpoint).hello()
        }
    }
}
