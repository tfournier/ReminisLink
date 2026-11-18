import Foundation
import Testing
@testable import ReminisLinkKit

@Suite("Models")
struct ModelsTests {
    func decode<T: Decodable>(_ type: T.Type, _ json: String) throws -> T {
        try ProtocolJSON.decoder().decode(T.self, from: Data(json.utf8))
    }

    @Test func helloFromContractExample() throws {
        let hello = try decode(Hello.self, """
        {"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"Brick de Thomas","model":"brick",
         "platform":"tg5040","os":"nextui","osVersion":"6.14.0","pakVersion":"1.0.0","apiVersion":1,
         "minClientVersion":"1.0.0","serverTime":"2026-11-18T17:00:00Z","pairingOpen":true}
        """)
        #expect(hello.model == .brick)
        #expect(hello.serverTime == Date(timeIntervalSince1970: 1_795_021_200))
        #expect(hello.pairingOpen)
    }

    @Test func deviceWithNullsAndUnknownFields() throws {
        let device = try decode(DeviceStatus.self, """
        {"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"Brick","model":"newmodel",
         "platform":"tg5040","os":"nextui","osVersion":null,"pakVersion":"1.0.0",
         "network":{"ipv4":"192.168.1.42","ssid":null},"battery":null,
         "storage":[{"id":"sd","label":"SD","totalBytes":127865454592,"freeBytes":40265318400,"extra":1}],
         "uptimeSeconds":512,"someFutureField":{"a":[1,2]}}
        """)
        #expect(device.osVersion == nil)
        #expect(device.battery == nil)
        #expect(device.network.ssid == nil)
        #expect(device.model.rawValue == "newmodel")
        #expect(device.model.displayName == "newmodel")
        #expect(device.storage.first?.usedBytes == 87_600_136_192, "\(String(describing: device.storage.first))")
    }

    @Test func deviceConnectedClients() throws {
        let device = try decode(DeviceStatus.self, """
        {"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"Brick","model":"brick",
         "platform":"tg5040","os":"nextui","osVersion":"6.14.0","pakVersion":"1.0.0",
         "network":{"ipv4":"192.168.1.42","ssid":"MaBox"},"battery":{"percent":78,"charging":false},
         "storage":[],"uptimeSeconds":512,
         "connectedClients":[{"clientName":"iPhone de Thomas","lastSeenAt":"2026-11-18T17:00:00Z","extra":1}]}
        """)
        #expect(device.connectedClients == [
            ConnectedClient(clientName: "iPhone de Thomas", lastSeenAt: Date(timeIntervalSince1970: 1_795_021_200)),
        ])
    }

    @Test func deviceWithoutConnectedClientsIsTolerated() throws {
        let device = try decode(DeviceStatus.self, """
        {"deviceId":"7f3a9c2e-1b4d-4c55-9a51-2f0f9e7d3a10","deviceName":"Brick","model":"brick",
         "platform":"tg5040","os":"nextui","osVersion":"6.14.0","pakVersion":"1.0.0",
         "network":{"ipv4":"192.168.1.42","ssid":null},"battery":null,"storage":[],"uptimeSeconds":1}
        """)
        #expect(device.connectedClients.isEmpty)
    }

    @Test func gameListingKeepsUnknownKinds() throws {
        let listing = try decode(GameListing.self, """
        {"folders":[{"id":"Zm9v","name":"Homebrew","gameCount":3}],
         "games":[{"id":"YmFy","name":"Sky Garden Quest","fileName":"Sky Garden Quest.gba","kind":"zip",
                   "sizeBytes":16777216,"modifiedAt":"2026-10-02T09:12:00.250Z","hasSave":true,"stateCount":3,
                   "lockedStateCount":1,"cheatCount":12,"hasArt":true,"unknown":true}]}
        """)
        #expect(listing.folders.first?.name == "Homebrew")
        #expect(listing.games.first?.kind == GameKind(rawValue: "zip"))
        #expect(listing.games.first?.lockedStateCount == 1)
    }

    @Test func gameDetailCounters() throws {
        let detail = try decode(GameDetail.self, """
        {"id":"YmFy","systemId":"Zm9v","name":"Sky Garden Quest","fileName":"Sky Garden Quest.gba","kind":"file",
         "files":[
          {"role":"rom","path":"Roms/GBA/Sky Garden Quest.gba","sizeBytes":16777216,"modifiedAt":"2026-10-02T09:12:00Z"},
          {"role":"save","path":"Saves/GBA/Sky Garden Quest.gba.sav","sizeBytes":131072,"modifiedAt":"2026-11-18T16:55:00Z"},
          {"role":"state","slot":0,"path":"x.st0","sizeBytes":401408,"modifiedAt":"2026-11-18T16:55:00Z"},
          {"role":"state","slot":1,"path":"x.st1","sizeBytes":401408,"modifiedAt":"2026-11-18T16:55:00Z"},
          {"role":"thumbnail","path":"x.png","sizeBytes":1,"modifiedAt":"2026-11-18T16:55:00Z"}
         ],"totalBytes":17377096}
        """)
        #expect(detail.hasSave)
        #expect(detail.stateCount == 2)
        #expect(!detail.hasCheats)
        #expect(detail.files[2].slot == 0)
        #expect(detail.files.last?.role == GameFileRole(rawValue: "thumbnail"))
    }

    @Test func badDateFails() {
        #expect(throws: DecodingError.self) {
            try decode(TrashEntry.self, """
            {"id":"t","trashedAt":"yesterday","reason":"delete-game","label":"x","files":[],"sizeBytes":1,"expiresAt":"2026-12-18T17:00:00Z"}
            """)
        }
    }

    @Test func dateRoundTrip() {
        let date = Date(timeIntervalSince1970: 1_795_021_200)
        #expect(ProtocolJSON.formatDate(date) == "2026-11-18T17:00:00Z")
        #expect(ProtocolJSON.parseDate("2026-11-18T17:00:00Z") == date)
        #expect(ProtocolJSON.parseDate("2026-11-18T17:00:00.5Z") == date.addingTimeInterval(0.5))
    }

    @Test func jsonValueIntegers() throws {
        let v = try JSONDecoder().decode([String: JSONValue].self, from: Data(#"{"a":35232153,"b":1.5,"c":true,"d":null,"e":"x"}"#.utf8))
        #expect(v["a"] == .integer(35_232_153))
        #expect(v["a"]?.int64Value == 35_232_153)
        #expect(v["b"]?.int64Value == nil)
        #expect(v["c"] == .bool(true))
        #expect(v["d"] == .null)
        #expect(v["e"] == .string("x"))
    }

    @Test func appVersionComparison() {
        #expect(AppVersion.isSupported("1.0.0", minimum: "1.0.0"))
        #expect(AppVersion.isSupported("1.2", minimum: "1.1.9"))
        #expect(AppVersion.isSupported("1.10.0", minimum: "1.9.0"))
        #expect(!AppVersion.isSupported("1.0.0", minimum: "1.0.1"))
        #expect(!AppVersion.isSupported("1.0.0", minimum: "2"))
        #expect(AppVersion.isSupported("1.0.0-beta", minimum: "1.0.0"))
    }
}
