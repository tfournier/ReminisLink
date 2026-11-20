import CryptoKit
import Foundation
import Testing
@testable import ReminisLinkKit

@Suite("Pairing")
struct PairingTests {
    let deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    let identity = ClientIdentity(
        clientId: "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", privateKey: Curve25519.Signing.PrivateKey()
    )

    /// Plays the device side with the same primitives.
    func deviceResponse(for request: PairRequest, secret: Data, deviceNonce: Data = Pairing.makeNonce()) -> PairResponse {
        let nonce = Base64URL.encode(deviceNonce)
        let message = Pairing.deviceProofMessage(
            deviceId: deviceId, clientId: request.clientId, clientPublicKey: request.clientPublicKey,
            clientNonce: request.clientNonce, deviceNonce: nonce
        )
        return PairResponse(
            deviceId: deviceId, deviceName: "Brick", deviceNonce: nonce,
            proof: Pairing.proof(secret: secret, message: message)
        )
    }

    @Test func clientIdIsLowercaseUUID() {
        let id = Pairing.makeClientId()
        #expect(id == id.lowercased())
        #expect(id.range(of: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", options: .regularExpression) != nil)
    }

    @Test func noncesAreRandom32Bytes() {
        let a = Pairing.makeNonce()
        #expect(a.count == 32)
        #expect(a != Pairing.makeNonce())
    }

    @Test func generatedIdentitiesDiffer() {
        let a = ClientIdentity.generate()
        let b = ClientIdentity.generate()
        #expect(a.clientId != b.clientId)
        #expect(a.publicKey != b.publicKey)
        #expect(Base64URL.decode(a.publicKey)?.count == 32)
    }

    @Test func fullExchange() throws {
        let secret = Pairing.makeNonce()
        let attempt = try PairingAttempt(secret: secret, deviceId: deviceId, identity: identity, clientName: "iPhone de Thomas")
        let request = attempt.request
        #expect(request.clientName == "iPhone de Thomas")
        #expect(request.clientPublicKey == identity.publicKey)
        // The client proof is the HMAC of the secret over the message.
        let message = Pairing.clientProofMessage(
            deviceId: deviceId, clientId: identity.clientId, clientPublicKey: identity.publicKey, clientNonce: request.clientNonce
        )
        #expect(Pairing.isValidProof(request.proof, secret: secret, message: message))
        try attempt.finish(deviceResponse(for: request, secret: secret))
    }

    @Test func requestEncodesAsTheContract() throws {
        let attempt = try PairingAttempt(secret: Pairing.makeNonce(), deviceId: deviceId, identity: identity, clientName: "iPhone")
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(attempt.request)) as? [String: String]
        #expect(Set(json?.keys ?? [:].keys) == ["clientId", "clientName", "platform", "clientPublicKey", "clientNonce", "proof"])
        #expect(json?["platform"] == "apple")
    }

    @Test func wrongDeviceProofIsRejected() throws {
        let attempt = try PairingAttempt(secret: Pairing.makeNonce(), deviceId: deviceId, identity: identity, clientName: "iPhone")
        // A device that does not know the secret.
        let forged = deviceResponse(for: attempt.request, secret: Pairing.makeNonce())
        #expect(throws: PairingError.invalidDeviceProof) { try attempt.finish(forged) }
    }

    @Test func proofCoversThePublicKey() throws {
        let secret = Pairing.makeNonce()
        let attempt = try PairingAttempt(secret: secret, deviceId: deviceId, identity: identity, clientName: "iPhone")
        // A proof made for another key (a man in the middle swapping it).
        let other = try PairingAttempt(
            secret: secret, deviceId: deviceId,
            identity: ClientIdentity(clientId: identity.clientId, privateKey: Curve25519.Signing.PrivateKey()),
            clientName: "iPhone", clientNonce: attempt.clientNonce
        )
        #expect(other.request.proof != attempt.request.proof)
        #expect(throws: PairingError.invalidDeviceProof) {
            try attempt.finish(deviceResponse(for: other.request, secret: secret))
        }
    }

    @Test func malformedResponsesAreRejected() throws {
        let secret = Pairing.makeNonce()
        let attempt = try PairingAttempt(secret: secret, deviceId: deviceId, identity: identity, clientName: "iPhone")
        let good = deviceResponse(for: attempt.request, secret: secret)
        let otherDevice = PairResponse(deviceId: "00000000-0000-4000-8000-000000000000", deviceName: "x", deviceNonce: good.deviceNonce, proof: good.proof)
        #expect(throws: PairingError.deviceMismatch) { try attempt.finish(otherDevice) }
        let shortNonce = PairResponse(deviceId: deviceId, deviceName: "x", deviceNonce: Base64URL.encode(Data(count: 16)), proof: good.proof)
        #expect(throws: PairingError.invalidDeviceNonce) { try attempt.finish(shortNonce) }
        let paddedProof = PairResponse(deviceId: deviceId, deviceName: "x", deviceNonce: good.deviceNonce, proof: good.proof + "=")
        #expect(throws: PairingError.invalidDeviceProof) { try attempt.finish(paddedProof) }
    }

    @Test func inputLengthsAreChecked() throws {
        #expect(throws: PairingError.invalidSecret) {
            try PairingAttempt(secret: Data(count: 31), deviceId: deviceId, identity: identity, clientName: "x")
        }
        #expect(throws: PairingError.invalidSecret) {
            try PairingAttempt(secret: Data(), deviceId: deviceId, identity: identity, clientName: "x")
        }
        #expect(throws: PairingError.invalidNonce) {
            try PairingAttempt(secret: Pairing.makeNonce(), deviceId: deviceId, identity: identity, clientName: "x", clientNonce: Data(count: 16))
        }
    }
}
