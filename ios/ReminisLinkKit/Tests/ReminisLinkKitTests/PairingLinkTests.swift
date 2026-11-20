import Foundation
import Testing
@testable import ReminisLinkKit

@Suite("Pairing link")
struct PairingLinkTests {
    let id = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    let secret = Base64URL.encode(Data(repeating: 7, count: 32))

    let pin = Base64URL.encode(Data(repeating: 9, count: 32))
    var tail: String { "s=\(secret)&h=192.168.1.42&p=47821&k=\(pin)" }

    @Test func parsesTheQRCodeLink() throws {
        let link = try PairingLink.parse("reminislink://pair?v=2&id=\(id)&n=Brick%20de%20Thomas&\(tail)")
        #expect(link.deviceId == id)
        #expect(link.deviceName == "Brick de Thomas")
        #expect(link.secret == Data(repeating: 7, count: 32))
        #expect(link.endpoint == Endpoint(host: "192.168.1.42", port: 47821))
        #expect(link.pin == pin)
    }

    @Test func plusIsASpaceInTheName() throws {
        let link = try PairingLink.parse("reminislink://pair?v=2&id=\(id)&n=Brick+de+Thomas&\(tail)")
        #expect(link.deviceName == "Brick de Thomas")
    }

    @Test func roundTrip() throws {
        let link = PairingLink(
            deviceId: id, deviceName: "Brick é 🎮", secret: Data(repeating: 1, count: 32),
            endpoint: Endpoint(host: "10.0.0.7", port: 47823), pin: pin
        )
        #expect(PairingLink(url: link.url) == link)
    }

    @Test func addressIsOptionalButWhole() throws {
        let link = try PairingLink.parse("reminislink://pair?v=2&id=\(id)&s=\(secret)&k=\(pin)")
        #expect(link.endpoint == nil)
        for hp in ["h=192.168.1.4", "p=47821", "h=example.com&p=47821", "h=192.168.1.4&p=0", "h=192.168.1.4&p=65536",
                   "h=192.168.01.4&p=47821", "h=192.168.1.256&p=47821", "h=::1&p=47821", "h=192.168.1.4&p=+1"] {
            #expect(throws: PairingLinkError.invalid, "\(hp)") {
                try PairingLink.parse("reminislink://pair?v=2&id=\(id)&s=\(secret)&k=\(pin)&\(hp)")
            }
        }
    }

    @Test func versionOneAsksToUpdateThePak() {
        // The link of a pak older than M13 (no pin): "update the pak".
        #expect(throws: PairingLinkError.outdatedPak) {
            try PairingLink.parse("reminislink://pair?v=1&id=\(id)&s=\(secret)&h=192.168.1.42&p=47821")
        }
        #expect(throws: PairingLinkError.newerVersion) {
            try PairingLink.parse("reminislink://pair?v=3&id=\(id)&\(tail)")
        }
    }

    @Test func rejectsInvalidLinks() {
        let bad = [
            "https://pair?v=2&id=\(id)&\(tail)",
            "reminislink://other?v=2&id=\(id)&\(tail)",
            "reminislink://pair/x?v=2&id=\(id)&\(tail)",
            "reminislink://pair?id=\(id)&\(tail)",
            "reminislink://pair?v=two&id=\(id)&\(tail)",
            "reminislink://pair?v=2&id=\(id.uppercased())&\(tail)",
            "reminislink://pair?v=2&id=not-a-uuid&\(tail)",
            "reminislink://pair?v=2&id=\(id)&s=\(Base64URL.encode(Data(repeating: 7, count: 16)))&k=\(pin)",
            "reminislink://pair?v=2&id=\(id)&s=\(secret)=&k=\(pin)",
            "reminislink://pair?v=2&id=\(id)&s=\(secret)",
            "reminislink://pair?v=2&id=\(id)&s=\(secret)&k=\(Base64URL.encode(Data(repeating: 9, count: 31)))",
            "reminislink://pair?v=2&id=\(id)&s=\(secret)&k=\(pin)=",
            "reminislink://pair?v=2&id=\(id)",
            "reminislink://pair?v=2&v=2&id=\(id)&\(tail)",
            "reminislink://pair?v=2&id=\(id)&\(tail)&k=\(pin)",
            "reminislink://pair?v=2&id=\(id)&\(tail)&n=%G1",
            "reminislink://pair?v=2&id=\(id)&\(tail)&n=%FF",
            "reminislink://pair",
            "reminislink://user@pair?v=2&id=\(id)&\(tail)",
            "reminislink://user:pw@pair?v=2&id=\(id)&\(tail)",
            "reminislink://pair:80?v=2&id=\(id)&\(tail)",
            "reminislink://pair?v=2&id=\(id)&\(tail)#frag",
        ]
        for string in bad {
            #expect(throws: PairingLinkError.invalid, "\(string)") { try PairingLink.parse(string) }
            #expect(PairingLink(string: string) == nil, "\(string)")
        }
    }

    @Test func clientNameIsCappedInUnicodeScalars() {
        #expect(Pairing.clientName(from: "  iPhone de Thomas ") == "iPhone de Thomas")
        #expect(Pairing.clientName(from: "   ") == "iPhone")
        let flags = String(repeating: "🇫🇷", count: 40) // 2 scalars each
        let name = Pairing.clientName(from: flags)
        #expect(name.unicodeScalars.count == 64)
        #expect(name == String(repeating: "🇫🇷", count: 32))
        let accents = String(repeating: "e\u{301}", count: 40) // decomposed é: 2 scalars, 1 character
        #expect(Pairing.clientName(from: accents).unicodeScalars.count == 64)
        #expect(Pairing.clientName(from: String(repeating: "a", count: 100)).count == 64)
    }

    @Test func endpointAuthority() {
        #expect(Endpoint(host: "192.168.1.42", port: 47821).authority == "192.168.1.42:47821")
        #expect(Endpoint(host: "fe80::1%en0", port: 47821).authority == "[fe80::1%25en0]:47821")
    }
}
