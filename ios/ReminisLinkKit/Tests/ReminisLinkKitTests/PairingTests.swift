import CryptoKit
import Foundation
import Testing
@testable import ReminisLinkKit

@Suite("Pairing")
struct PairingTests {
    let deviceId = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    let clientId = "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d"

    /// Plays the device side with the same primitives.
    func deviceResponse(for request: PairRequest, ikm: Data, deviceNonce: Data = Pairing.makeNonce()) throws -> PairResponse {
        let clientNonce = try #require(Base64URL.decode(request.clientNonce))
        let key = Pairing.deriveKey(ikm: ikm, clientNonce: clientNonce, deviceNonce: deviceNonce, deviceId: deviceId, clientId: request.clientId)
        return PairResponse(
            deviceId: deviceId, deviceName: "Brick",
            deviceNonce: Base64URL.encode(deviceNonce),
            proof: Pairing.deviceProof(key: key, deviceId: deviceId, clientId: request.clientId)
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

    @Test func pinIKMIsUTF8() {
        #expect(PairingSecret.pin("042517").ikm == Data("042517".utf8))
        #expect(PairingSecret.pin("042517").method == .pin)
        #expect(PairingSecret.qr(Data(count: 32)).method == .qr)
    }

    @Test func fullExchangeQR() throws {
        let secret = Pairing.makeNonce()
        let attempt = PairingAttempt(secret: .qr(secret), deviceId: deviceId, clientId: clientId, clientName: "iPhone de Thomas")
        let request = attempt.request
        #expect(request.clientName == "iPhone de Thomas")
        let response = try deviceResponse(for: request, ikm: secret)
        let key = try attempt.finish(response)
        #expect(key.bitCount == 256)
    }

    @Test func requestEncodesAsTheContract() throws {
        let attempt = PairingAttempt(secret: .pin("123456"), deviceId: deviceId, clientId: clientId, clientName: "iPhone")
        let json = try JSONSerialization.jsonObject(with: JSONEncoder().encode(attempt.request)) as? [String: String]
        #expect(Set(json?.keys ?? [:].keys) == ["method", "clientId", "clientName", "clientNonce", "proof"])
        #expect(json?["method"] == "pin")
    }

    @Test func wrongDeviceProofIsRejected() throws {
        let secret = Pairing.makeNonce()
        let attempt = PairingAttempt(secret: .qr(secret), deviceId: deviceId, clientId: clientId, clientName: "iPhone")
        // A device that does not know the secret.
        let forged = try deviceResponse(for: attempt.request, ikm: Pairing.makeNonce())
        #expect(throws: PairingError.invalidDeviceProof) { try attempt.finish(forged) }
    }

    @Test func malformedResponsesAreRejected() throws {
        let secret = Pairing.makeNonce()
        let attempt = PairingAttempt(secret: .qr(secret), deviceId: deviceId, clientId: clientId, clientName: "iPhone")
        let good = try deviceResponse(for: attempt.request, ikm: secret)
        let otherDevice = PairResponse(deviceId: "00000000-0000-4000-8000-000000000000", deviceName: "x", deviceNonce: good.deviceNonce, proof: good.proof)
        #expect(throws: PairingError.deviceMismatch) { try attempt.finish(otherDevice) }
        let shortNonce = PairResponse(deviceId: deviceId, deviceName: "x", deviceNonce: Base64URL.encode(Data(count: 16)), proof: good.proof)
        #expect(throws: PairingError.invalidDeviceNonce) { try attempt.finish(shortNonce) }
        let paddedProof = PairResponse(deviceId: deviceId, deviceName: "x", deviceNonce: good.deviceNonce, proof: good.proof + "=")
        #expect(throws: PairingError.invalidDeviceProof) { try attempt.finish(paddedProof) }
    }
}
