import CryptoKit
import Foundation
import Testing
@testable import ReminisLinkKit

@Suite("Shared test vectors")
struct VectorTests {
    @Test func pairing() throws {
        let file = try Vectors.load("pairing", as: PairingVectors.self)
        #expect(file.cases.filter { $0.method == "qr" }.count >= 3)
        #expect(file.cases.filter { $0.method == "pin" }.count >= 2)
        for c in file.cases {
            let secret: PairingSecret
            switch c.method {
            case "qr":
                let encoded = try #require(c.secret, "\(c.name)")
                secret = .qr(try #require(Base64URL.decode(encoded), "\(c.name)"))
            case "pin": secret = .pin(try #require(c.pin))
            default: Issue.record("\(c.name): unknown method \(c.method)"); continue
            }
            #expect(secret.ikm.hex == c.ikmHex, "\(c.name)")
            #expect(Pairing.clientProofMessage(deviceId: c.deviceId, clientId: c.clientId, clientNonce: c.clientNonce) == c.clientProofMessage)
            #expect(Pairing.clientProof(ikm: secret.ikm, deviceId: c.deviceId, clientId: c.clientId, clientNonce: c.clientNonce) == c.clientProof, "\(c.name)")

            let clientNonce = try #require(Base64URL.decode(c.clientNonce))
            let deviceNonce = try #require(Base64URL.decode(c.deviceNonce))
            #expect((clientNonce + deviceNonce).hex == c.saltHex)
            #expect(Pairing.keyInfo(deviceId: c.deviceId, clientId: c.clientId) == c.keyInfo)
            let key = Pairing.deriveKey(ikm: secret.ikm, clientNonce: clientNonce, deviceNonce: deviceNonce, deviceId: c.deviceId, clientId: c.clientId)
            #expect(key.withUnsafeBytes { Base64URL.encode(Data($0)) } == c.key, "\(c.name)")
            #expect(Pairing.deviceProofMessage(deviceId: c.deviceId, clientId: c.clientId) == c.deviceProofMessage)
            #expect(Pairing.deviceProof(key: key, deviceId: c.deviceId, clientId: c.clientId) == c.deviceProof, "\(c.name)")
            #expect(Pairing.isValidDeviceProof(c.deviceProof, key: key, deviceId: c.deviceId, clientId: c.clientId))

            // Full exchange as the app runs it.
            let attempt = PairingAttempt(secret: secret, deviceId: c.deviceId, clientId: c.clientId, clientName: "iPhone", clientNonce: clientNonce)
            #expect(attempt.request.method.rawValue == c.method)
            #expect(attempt.request.clientNonce == c.clientNonce)
            #expect(attempt.request.proof == c.clientProof)
            let finished = try attempt.finish(PairResponse(deviceId: c.deviceId, deviceName: "Brick", deviceNonce: c.deviceNonce, proof: c.deviceProof))
            #expect(finished.withUnsafeBytes { Base64URL.encode(Data($0)) } == c.key)
        }
    }

    @Test func signing() throws {
        let file = try Vectors.load("signing", as: SigningVectors.self)
        #expect(file.cases.count >= 8)
        for c in file.cases {
            let key = SymmetricKey(data: try #require(Base64URL.decode(c.key)))
            if c.contentSha256 != Signer.unsignedPayload {
                #expect(Signer.contentSHA256(Data(c.body.utf8)) == c.contentSha256, "\(c.name)")
            }
            #expect(try Signer.canonicalQuery(rawQuery: c.query) == c.canonicalQuery, "\(c.name)")
            let canonical = try Signer.canonicalRequest(
                method: c.method, path: c.path, rawQuery: c.query,
                time: c.time, nonce: c.nonce, contentSHA256: c.contentSha256
            )
            #expect(canonical == c.canonicalRequest, "\(c.name)")
            #expect(Signer.signature(key: key, canonical: canonical) == c.signature, "\(c.name)")
            #expect(Signer.isValidSignature(c.signature, key: key, canonical: canonical))

            // The high-level API gives the same headers.
            let signer = Signer(clientId: c.clientId, key: key)
            let headers = try signer.headers(
                method: c.method, path: c.path, rawQuery: c.query, body: Data(c.body.utf8),
                unsignedBody: c.contentSha256 == Signer.unsignedPayload,
                time: try #require(Int64(c.time)), nonce: c.nonce
            )
            #expect(headers.dictionary == c.headers, "\(c.name)")
        }
        for q in file.invalidQueries {
            #expect(throws: SignerError.malformedQuery, "\(q.name)") { try Signer.canonicalQuery(rawQuery: q.query) }
        }
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
}
