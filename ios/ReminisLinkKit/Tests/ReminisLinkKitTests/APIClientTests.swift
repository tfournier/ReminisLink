import CryptoKit
import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// A fake pak on a host of its own, so the tests can run in parallel.
func makePak(minClientVersion: String = "1.0.0") -> FakePak {
    let pak = FakePak(host: "pak-\(UUID().uuidString.lowercased()).test", minClientVersion: minClientVersion)
    pak.register()
    return pak
}

/// Pairs with the QR secret and returns a signed client.
func pairedClient(_ pak: FakePak, now: @escaping @Sendable () -> Date = { Date() }) async throws -> APIClient {
    let session = pak.makeSession()
    let unsigned = APIClient(endpoint: pak.endpoint, expectedDeviceId: pak.deviceId, session: session)
    let clientId = Pairing.makeClientId()
    let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "Test iPhone")
    let key = try await unsigned.pair(attempt)
    return APIClient(
        endpoint: pak.endpoint,
        credentials: DeviceCredentials(deviceId: pak.deviceId, clientId: clientId, key: key),
        session: session, now: now
    )
}

/// A client whose requests are answered by a closure.
func stubClient(credentials: Bool = true, _ handler: @escaping @Sendable (URLRequest, Data) -> FakeResponse) -> APIClient {
    let host = "stub-\(UUID().uuidString.lowercased()).test"
    FakeURLProtocol.register(ClosureHandler(handler), host: host)
    let creds = credentials
        ? DeviceCredentials(deviceId: FakePak.demoDeviceId, clientId: Pairing.makeClientId(), key: SymmetricKey(size: .bits256))
        : nil
    return APIClient(endpoint: Endpoint(host: host, port: 47821), credentials: creds,
                     session: APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]))
}

@Suite("API client")
struct APIClientTests {
    @Test func helloMeasuresTheClockOffset() async throws {
        let pak = makePak()
        pak.clockOffset = 1000
        let client = APIClient(endpoint: pak.endpoint, expectedDeviceId: pak.deviceId, session: pak.makeSession())
        let hello = try await client.hello()
        #expect(hello.deviceId == pak.deviceId)
        let offset = await client.clockOffset
        #expect(abs(offset - 1000) < 3)
    }

    @Test func helloRejectsAnotherDevice() async throws {
        let pak = makePak()
        let other = "00000000-0000-4000-8000-000000000000"
        let client = APIClient(endpoint: pak.endpoint, expectedDeviceId: other, session: pak.makeSession())
        await #expect(throws: APIError.deviceMismatch(expected: other, actual: pak.deviceId)) {
            try await client.hello()
        }
    }

    @Test func helloRejectsATooOldApp() async throws {
        let pak = makePak(minClientVersion: "2.0.0")
        let client = APIClient(endpoint: pak.endpoint, appVersion: "1.0.0", session: pak.makeSession())
        await #expect(throws: APIError.incompatible(apiVersion: 1, minClientVersion: "2.0.0")) {
            try await client.hello()
        }
    }

    @Test func pairsWithTheQRSecretThenSigns() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        #expect(pak.clientIds.count == 1)
        let device = try await client.device()
        #expect(device.deviceId == pak.deviceId)
        #expect(device.battery?.percent == 78)
        #expect(device.connectedClients.map(\.clientName) == ["Test iPhone"])
        let last = try #require(pak.log.last)
        #expect(last.path == "/api/v1/device")
        #expect(last.status == 200)
    }

    @Test func pairsWithThePIN() async throws {
        let pak = makePak()
        let client = APIClient(endpoint: pak.endpoint, session: pak.makeSession())
        let attempt = try PairingAttempt(secret: .pin(pak.pin), deviceId: pak.deviceId, clientId: Pairing.makeClientId(), clientName: "iPhone")
        let key = try await client.pair(attempt)
        #expect(key.bitCount == 256)
    }

    @Test func wrongSecretGivesPairingFailed() async throws {
        let pak = makePak()
        let client = APIClient(endpoint: pak.endpoint, session: pak.makeSession())
        let attempt = try PairingAttempt(secret: .pin("000000"), deviceId: pak.deviceId, clientId: Pairing.makeClientId(), clientName: "iPhone")
        do {
            _ = try await client.pair(attempt)
            Issue.record("expected an error")
        } catch let APIError.server(error) {
            #expect(error.status == 401)
            #expect(error.code == .pairingFailed)
        }
    }

    @Test func forgedDeviceProofIsRejected() async throws {
        let client = stubClient(credentials: false) { request, body in
            let pair = try! JSONDecoder().decode(PairRequest.self, from: body)
            let nonce = Pairing.makeNonce()
            // A device that does not know the secret derives another key.
            let key = try! Pairing.deriveKey(ikm: Pairing.makeNonce(), clientNonce: Base64URL.decode(pair.clientNonce)!,
                                             deviceNonce: nonce, deviceId: FakePak.demoDeviceId, clientId: pair.clientId)
            return .json(200, ["deviceId": FakePak.demoDeviceId, "deviceName": "x", "deviceNonce": Base64URL.encode(nonce),
                               "proof": Pairing.deviceProof(key: key, deviceId: FakePak.demoDeviceId, clientId: pair.clientId)])
        }
        let attempt = try PairingAttempt(secret: .qr(Pairing.makeNonce()), deviceId: FakePak.demoDeviceId,
                                         clientId: Pairing.makeClientId(), clientName: "iPhone")
        await #expect(throws: APIError.pairing(.invalidDeviceProof)) { try await client.pair(attempt) }
    }

    @Test func clockSkewIsCorrectedOnceWithANewNonce() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        pak.clockOffset = 3600
        let systems = try await client.systems()
        #expect(systems.count == 3)
        #expect(systems.map(\.emulatorInstalled) == [true, true, false], "\(systems.map(\.tag))")
        let signed = pak.log.filter { $0.path == "/api/v1/systems" }
        #expect(signed.map(\.status) == [401, 200])
        #expect(signed[0].nonce != signed[1].nonce)
        #expect(pak.log.contains { $0.path == "/api/v1/hello" })
        // The offset is kept for the next requests.
        _ = try await client.device()
        #expect(pak.log.last?.status == 200)
        #expect(pak.log.filter { $0.status == 401 }.count == 1)
    }

    @Test func clockSkewTwiceIsReported() async throws {
        let calls = Counter()
        let client = stubClient { request, _ in
            if request.url?.path() == "/api/v1/hello" {
                return .json(200, ["deviceId": FakePak.demoDeviceId, "deviceName": "x", "model": "brick", "platform": "tg5040",
                                   "os": "nextui", "osVersion": NSNull(), "pakVersion": "1.0.0", "apiVersion": 1,
                                   "minClientVersion": "1.0.0", "serverTime": "2026-11-18T17:00:00Z", "pairingOpen": false])
            }
            calls.increment()
            return .error(401, code: "clock_skew")
        }
        await #expect(throws: APIError.server(ServerError(status: 401, code: .clockSkew, message: "error"))) {
            try await client.systems()
        }
        #expect(calls.value == 2)
    }

    @Test func gamesSendsACanonicalQuery() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let system = try #require(try await client.systems().first { $0.tag == "GBA" })
        let root = try await client.games(systemId: system.id)
        #expect(root.folders.map(\.name) == ["Homebrew"])
        #expect(root.games.map(\.name).sorted() == ["Pixel Runner", "Sky Garden Quest"])
        let folder = try await client.games(systemId: system.id, folderId: root.folders[0].id)
        #expect(folder.games.count == 2)
        let last = try #require(pak.log.last)
        #expect(last.query == "folderId=" + root.folders[0].id)
        #expect(try Signer.canonicalQuery(rawQuery: last.query) == last.query)
        let detail = try await client.game(id: folder.games[0].id)
        #expect(detail.files.first?.role == .rom)
    }

    @Test func unknownClient() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        try await client.unpair()
        #expect(pak.clientIds.isEmpty)
        do {
            _ = try await client.device()
            Issue.record("expected an error")
        } catch {
            #expect((error as? APIError)?.code == .unknownClient)
        }
    }

    @Test func signedRouteWithoutCredentials() async throws {
        let pak = makePak()
        let client = APIClient(endpoint: pak.endpoint, session: pak.makeSession())
        await #expect(throws: APIError.notPaired) { try await client.device() }
    }

    @Test func trashNeedsTheConfirmation() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let trash = try await client.trash()
        #expect(trash.entries.count == 1)
        let freed = try await client.deleteTrashEntry(id: trash.entries[0].id)
        #expect(freed.freedBytes == trash.totalBytes)
        #expect(try await client.emptyTrash().freedBytes == 0)
        #expect(try await client.trash().entries.isEmpty)
    }

    /// A redirection would resend the request outside the signing loop
    /// (same nonce, maybe the body, maybe off the local network): refused.
    @Test(arguments: [301, 302, 307, 308])
    func redirectionsAreNeverFollowed(status: Int) async throws {
        let calls = Counter()
        let client = stubClient { request, _ in
            calls.increment()
            if request.url?.path() == "/api/v1/device" {
                return FakeResponse(status: status, headers: ["Location": "/api/v1/elsewhere"])
            }
            return .json(200, [:])
        }
        await #expect(throws: APIError.unexpectedStatus(status)) { try await client.device() }
        #expect(calls.value == 1)
        // Unsigned routes too (the body of /pair).
        let attempt = try PairingAttempt(secret: .qr(Pairing.makeNonce()), deviceId: FakePak.demoDeviceId,
                                         clientId: Pairing.makeClientId(), clientName: "iPhone")
        let pairCalls = Counter()
        let unsigned = stubClient(credentials: false) { _, _ in
            pairCalls.increment()
            return FakeResponse(status: status, headers: ["Location": "https://example.org/collect"])
        }
        await #expect(throws: APIError.unexpectedStatus(status)) { try await unsigned.pair(attempt) }
        #expect(pairCalls.value == 1)
    }

    /// The fake answers above do redirect a session that follows them, so
    /// the test above checks the client, not the fake.
    @Test func aPlainSessionFollowsTheFakeRedirection() async throws {
        let host = "stub-\(UUID().uuidString.lowercased()).test"
        let calls = Counter()
        FakeURLProtocol.register(ClosureHandler { request, _ in
            calls.increment()
            if request.url?.path() == "/a" { return FakeResponse(status: 307, headers: ["Location": "/b"]) }
            return .json(200, [:])
        }, host: host)
        let session = APIClient.makeSession(protocolClasses: [FakeURLProtocol.self])
        let (_, response) = try await session.data(from: URL(string: "http://\(host)/a")!)
        #expect((response as? HTTPURLResponse)?.statusCode == 200)
        #expect(calls.value == 2)
    }

    @Test func artIsCachedWithItsETag() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let cache = ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: "art-\(UUID().uuidString)"))
        defer { try? FileManager.default.removeItem(at: cache.directory) }
        let system = try #require(try await client.systems().first { $0.tag == "GBA" })
        let games = try await client.games(systemId: system.id).games
        let withArt = try #require(games.first { $0.hasArt })
        let withoutArt = try #require(games.first { !$0.hasArt })

        #expect(try await cache.load(gameId: withArt.id, client: client) == FakePak.artPNG)
        #expect(pak.log.last?.status == 200)
        #expect(cache.entry(deviceId: pak.deviceId, gameId: withArt.id)?.etag == FakePak.artETag)
        #expect(try await cache.load(gameId: withArt.id, client: client) == FakePak.artPNG)
        #expect(pak.log.last?.status == 304)
        #expect(try await cache.load(gameId: withoutArt.id, client: client) == nil)
    }
}

final class Counter: @unchecked Sendable {
    private let lock = NSLock()
    private var count = 0
    func increment() { lock.withLock { count += 1 } }
    var value: Int { lock.withLock { count } }
}
