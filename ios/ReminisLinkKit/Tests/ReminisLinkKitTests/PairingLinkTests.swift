import Foundation
import Testing
@testable import ReminisLinkKit

@Suite("Pairing link")
struct PairingLinkTests {
    let id = "7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10"
    let secret = Base64URL.encode(Data(repeating: 7, count: 32))

    @Test func parsesTheQRCodeLink() throws {
        let link = try #require(PairingLink(string: "reminislink://pair?v=1&id=\(id)&n=Brick%20de%20Thomas&s=\(secret)&h=192.168.1.42&p=47821"))
        #expect(link.deviceId == id)
        #expect(link.deviceName == "Brick de Thomas")
        #expect(link.secret == Data(repeating: 7, count: 32))
        #expect(link.fallbackEndpoint == Endpoint(host: "192.168.1.42", port: 47821))
    }

    @Test func plusIsASpaceInTheName() throws {
        let link = try #require(PairingLink(string: "reminislink://pair?v=1&id=\(id)&n=Brick+de+Thomas&s=\(secret)"))
        #expect(link.deviceName == "Brick de Thomas")
        #expect(link.fallbackEndpoint == nil)
    }

    @Test func roundTrip() throws {
        let link = PairingLink(deviceId: id, deviceName: "Brick é 🎮", secret: Data(repeating: 1, count: 32),
                               fallbackEndpoint: Endpoint(host: "10.0.0.2", port: 47822))
        #expect(PairingLink(url: link.url) == link)
    }

    @Test func invalidEndpointIsIgnored() throws {
        for hp in ["h=192.168.1.256&p=47821", "h=8.8.8.8&p=47821", "h=172.32.0.1&p=47821", "h=127.0.0.1&p=47821", "h=example.com&p=47821", "h=192.168.1.4&p=0", "h=192.168.1.4&p=70000", "h=01.2.3.4&p=1", "h=192.168.1.4"] {
            let link = try #require(PairingLink(string: "reminislink://pair?v=1&id=\(id)&s=\(secret)&\(hp)"), "\(hp)")
            #expect(link.fallbackEndpoint == nil, "\(hp)")
        }
    }

    @Test func rejectsInvalidLinks() {
        let bad = [
            "https://pair?v=1&id=\(id)&s=\(secret)",
            "reminislink://other?v=1&id=\(id)&s=\(secret)",
            "reminislink://pair/x?v=1&id=\(id)&s=\(secret)",
            "reminislink://pair?v=2&id=\(id)&s=\(secret)",
            "reminislink://pair?id=\(id)&s=\(secret)",
            "reminislink://pair?v=1&id=\(id.uppercased())&s=\(secret)",
            "reminislink://pair?v=1&id=not-a-uuid&s=\(secret)",
            "reminislink://pair?v=1&id=\(id)&s=\(Base64URL.encode(Data(repeating: 7, count: 16)))",
            "reminislink://pair?v=1&id=\(id)&s=\(secret)=",
            "reminislink://pair?v=1&id=\(id)",
            "reminislink://pair?v=1&v=1&id=\(id)&s=\(secret)",
            "reminislink://pair?v=1&id=\(id)&s=\(secret)&n=%G1",
            "reminislink://pair?v=1&id=\(id)&s=\(secret)&n=%FF",
            "reminislink://pair",
            "reminislink://user@pair?v=1&id=\(id)&s=\(secret)",
            "reminislink://user:pw@pair?v=1&id=\(id)&s=\(secret)",
            "reminislink://pair:80?v=1&id=\(id)&s=\(secret)",
            "reminislink://pair?v=1&id=\(id)&s=\(secret)#frag",
        ]
        for string in bad {
            #expect(PairingLink(string: string) == nil, "\(string)")
        }
    }

    @Test func onlyLocalIPv4Fallbacks() {
        for ok in ["10.0.0.1", "10.255.255.255", "172.16.0.1", "172.31.255.1", "192.168.1.42", "169.254.10.2"] {
            #expect(PairingLink.isLocalIPv4(ok), "\(ok)")
        }
        for bad in ["8.8.8.8", "172.15.0.1", "172.32.0.1", "192.169.0.1", "127.0.0.1", "169.253.0.1", "0.0.0.0", "1.2.3"] {
            #expect(!PairingLink.isLocalIPv4(bad), "\(bad)")
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
