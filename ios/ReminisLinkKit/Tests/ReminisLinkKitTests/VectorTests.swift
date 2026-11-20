import CryptoKit
import Foundation
import Security
import Testing
@testable import ReminisLinkKit

@Suite("Shared test vectors")
struct VectorTests {
    @Test func pairing() throws {
        let file = try Vectors.load("pairing", as: PairingVectors.self)
        #expect(file.cases.count >= 3)
        for c in file.cases {
            let secret = try #require(Base64URL.decode(c.secret), "\(c.name)")
            #expect(secret.hex == c.secretHex, "\(c.name)")
            let seed = try #require(Base64URL.decode(c.clientSeed))
            let identity = ClientIdentity(
                clientId: c.clientId, privateKey: try Curve25519.Signing.PrivateKey(rawRepresentation: seed)
            )
            #expect(identity.publicKey == c.clientPublicKey, "\(c.name)")
            #expect(Pairing.clientProofMessage(
                deviceId: c.deviceId, clientId: c.clientId, clientPublicKey: c.clientPublicKey, clientNonce: c.clientNonce
            ) == c.clientProofMessage, "\(c.name)")
            #expect(Pairing.proof(secret: secret, message: c.clientProofMessage) == c.clientProof, "\(c.name)")
            #expect(Pairing.deviceProofMessage(
                deviceId: c.deviceId, clientId: c.clientId, clientPublicKey: c.clientPublicKey,
                clientNonce: c.clientNonce, deviceNonce: c.deviceNonce
            ) == c.deviceProofMessage, "\(c.name)")
            #expect(Pairing.proof(secret: secret, message: c.deviceProofMessage) == c.deviceProof, "\(c.name)")
            #expect(Pairing.isValidProof(c.deviceProof, secret: secret, message: c.deviceProofMessage))

            // Full exchange as the app runs it.
            let attempt = try PairingAttempt(
                secret: secret, deviceId: c.deviceId, identity: identity, clientName: "iPhone",
                clientNonce: try #require(Base64URL.decode(c.clientNonce))
            )
            #expect(attempt.request.platform == .apple)
            #expect(attempt.request.clientPublicKey == c.clientPublicKey)
            #expect(attempt.request.clientNonce == c.clientNonce)
            #expect(attempt.request.proof == c.clientProof, "\(c.name)")
            try attempt.finish(PairResponse(deviceId: c.deviceId, deviceName: "Brick", deviceNonce: c.deviceNonce, proof: c.deviceProof))
            // A proof made for another device nonce is refused.
            #expect(throws: PairingError.invalidDeviceProof) {
                try attempt.finish(PairResponse(
                    deviceId: c.deviceId, deviceName: "Brick", deviceNonce: c.clientNonce, proof: c.deviceProof
                ))
            }
        }
    }

    @Test func pairingLinks() throws {
        let file = try Vectors.load("pairing", as: PairingVectors.self)
        #expect(file.links.count >= 2)
        for v in file.links {
            let link = try PairingLink.parse(v.link)
            #expect(link.deviceId == v.deviceId, "\(v.name)")
            #expect(link.deviceName == v.deviceName, "\(v.name)")
            #expect(link.secret == Base64URL.decode(v.secret), "\(v.name)")
            #expect(link.endpoint == Endpoint(host: v.host, port: v.port), "\(v.name)")
            #expect(link.pin == v.pin, "\(v.name)")
            // Built back exactly as the pak encodes it.
            #expect(link.url.absoluteString == v.link, "\(v.name)")
        }
    }

    @Test func signing() throws {
        let file = try Vectors.load("signing", as: SigningVectors.self)
        #expect(file.cases.count >= 8)
        for c in file.cases {
            let privateKey = try Curve25519.Signing.PrivateKey(rawRepresentation: try #require(Base64URL.decode(c.seed)))
            #expect(Base64URL.encode(privateKey.publicKey.rawRepresentation) == c.publicKey, "\(c.name)")
            if c.contentSha256 != Signer.unsignedPayload {
                #expect(Signer.contentSHA256(Data(c.body.utf8)) == c.contentSha256, "\(c.name)")
            }
            #expect(try Signer.canonicalQuery(rawQuery: c.query) == c.canonicalQuery, "\(c.name)")
            let canonical = try Signer.canonicalRequest(
                method: c.method, path: c.path, rawQuery: c.query,
                time: c.time, nonce: c.nonce, contentSHA256: c.contentSha256
            )
            #expect(canonical == c.canonicalRequest, "\(c.name)")
            #expect(canonical.hasPrefix("RL2-ED25519\n"))
            // The deterministic signature of the vector verifies; CryptoKit
            // adds randomness, so its own signature is checked the same way.
            #expect(Signer.isValidSignature(c.signature, publicKey: privateKey.publicKey, canonical: canonical), "\(c.name)")
            let ours = try Signer.signature(privateKey: privateKey, canonical: canonical)
            #expect(Base64URL.decode(ours)?.count == 64)
            #expect(Signer.isValidSignature(ours, publicKey: privateKey.publicKey, canonical: canonical), "\(c.name)")
            #expect(!Signer.isValidSignature(ours, publicKey: privateKey.publicKey, canonical: canonical + "x"))

            // The high-level API gives the same headers, but the signature.
            let signer = Signer(clientId: c.clientId, privateKey: privateKey)
            let headers = try signer.headers(
                method: c.method, path: c.path, rawQuery: c.query, body: Data(c.body.utf8),
                unsignedBody: c.contentSha256 == Signer.unsignedPayload,
                time: try #require(Int64(c.time)), nonce: c.nonce
            )
            var expected = c.headers
            var actual = headers.dictionary
            expected[SignedHeaders.signature] = nil
            actual[SignedHeaders.signature] = nil
            #expect(actual == expected, "\(c.name)")
            #expect(Signer.isValidSignature(headers.signature, publicKey: privateKey.publicKey, canonical: canonical))
        }
        for q in file.invalidQueries {
            #expect(throws: SignerError.malformedQuery, "\(q.name)") { try Signer.canonicalQuery(rawQuery: q.query) }
        }
    }

    @Test func certificatePin() throws {
        let v = try Vectors.load("tls", as: TLSVectors.self)
        let point = try #require(Data(hex: v.publicKeyX963Hex))
        let spki = try #require(TLSPin.spki(x963: point))
        #expect(spki.hex == v.spkiDerHex)
        #expect(TLSPin.pin(spki: spki) == v.pin)
        #expect(Data(SHA256.hash(data: spki)).base64EncodedString() == v.pinStdBase64)

        // From the certificate, as the app reads it from a TLS handshake.
        let certificate = try #require(Self.certificate(pem: v.certificatePem))
        #expect(TLSPin.pin(certificate: certificate) == v.pin)
        let key = try #require(SecCertificateCopyKey(certificate))
        #expect((SecKeyCopyExternalRepresentation(key, nil) as Data?)?.hex == v.publicKeyX963Hex)
        #expect(TLSPin.pin(publicKey: key) == v.pin)

        // The private key of the vector gives the same point.
        let privateKey = try P256.Signing.PrivateKey(rawRepresentation: try #require(Data(hex: v.privateKeyHex)))
        #expect(privateKey.publicKey.x963Representation.hex == v.publicKeyX963Hex)
        #expect(TLSPin.pin(spki: privateKey.publicKey.derRepresentation) == v.pin)
    }

    @Test func identifiers() throws {
        let file = try Vectors.load("ids", as: IDVectors.self)
        #expect(file.valid.count >= 10)
        for v in file.valid {
            #expect(Data(v.path.utf8).hex == v.pathUtf8Hex, "\(v.name): JSON decoding changed the bytes")
            #expect(Base64URL.encode(Data(v.path.utf8)) == v.id, "\(v.name)")
            #expect(Base64URL.decode(v.id) == Data(v.path.utf8), "\(v.name)")
        }
        // The app never decodes ids, but its base64url decoder must refuse
        // the same malformed encodings as the pak.
        for v in file.invalid where v.reason == "invalid base64url" {
            #expect(Base64URL.decode(v.id) == nil, "\(v.name)")
        }
    }

    /// A certificate from PEM.
    static func certificate(pem: String) -> SecCertificate? {
        let body = pem.split(separator: "\n").filter { !$0.hasPrefix("-----") }.joined()
        guard let der = Data(base64Encoded: body) else { return nil }
        return SecCertificateCreateWithData(nil, der as CFData)
    }
}
