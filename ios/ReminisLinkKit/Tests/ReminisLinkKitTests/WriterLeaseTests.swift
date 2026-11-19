import CryptoKit
import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// A paired client with its own name, and its id (to disconnect or forget
/// it from the "console").
func namedClient(
    _ pak: FakePak, name: String, errorObserver: (@Sendable (ServerError, ContinuousClock.Instant) -> Void)? = nil
) async throws -> (client: APIClient, clientId: String) {
    let session = pak.makeSession()
    let unsigned = APIClient(endpoint: pak.endpoint, expectedDeviceId: pak.deviceId, session: session)
    let clientId = Pairing.makeClientId()
    let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: name)
    let key = try await unsigned.pair(attempt)
    let client = APIClient(
        endpoint: pak.endpoint,
        credentials: DeviceCredentials(deviceId: pak.deviceId, clientId: clientId, key: key),
        session: session, errorObserver: errorObserver
    )
    return (client, clientId)
}

/// A clock the tests move by hand.
final class MutableClock: @unchecked Sendable {
    private let lock = NSLock()
    private var value: Date
    init(_ start: Date = Date()) { value = start }
    var now: Date { lock.withLock { value } }
    func advance(_ seconds: TimeInterval) { lock.withLock { value = value.addingTimeInterval(seconds) } }
}

/// The waits of a ``WriterLease`` between renewals, ended by hand.
final class ManualSleeper: @unchecked Sendable {
    private let lock = NSLock()
    private var waiters: [(id: UUID, continuation: CheckedContinuation<Void, Error>)] = []
    private var _requested: [Duration] = []

    var requested: [Duration] { lock.withLock { _requested } }
    var pending: Int { lock.withLock { waiters.count } }

    func sleep(_ duration: Duration) async throws {
        let id = UUID()
        try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
                lock.withLock {
                    _requested.append(duration)
                    waiters.append((id, continuation))
                }
            }
        } onCancel: {
            let waiter = self.lock.withLock { () -> CheckedContinuation<Void, Error>? in
                guard let index = self.waiters.firstIndex(where: { $0.id == id }) else { return nil }
                return self.waiters.remove(at: index).continuation
            }
            waiter?.resume(throwing: CancellationError())
        }
    }

    /// Ends the oldest wait (one renewal interval goes by).
    func fire() {
        let waiter = lock.withLock { waiters.isEmpty ? nil : waiters.removeFirst().continuation }
        waiter?.resume()
    }
}

func waitFor(timeout: Duration = .seconds(5), _ condition: @MainActor () -> Bool) async {
    let deadline = ContinuousClock.now + timeout
    while await !condition(), ContinuousClock.now < deadline {
        try? await Task.sleep(for: .milliseconds(10))
    }
}

func writerRequests(_ pak: FakePak, _ method: String) -> Int {
    pak.log.filter { $0.method == method && $0.path == "/api/v1/writer" }.count
}

// MARK: - Fake pak

@Suite("Fake pak write lease (M10)")
struct FakePakWriterTests {
    @Test func firstWriteTakesTheFreeLeaseAndOthersAreReadOnly() async throws {
        let pak = makePak()
        let (a, _) = try await namedClient(pak, name: "iPhone A")
        let (b, _) = try await namedClient(pak, name: "iPhone B")
        #expect(try await a.hello().writerHeld == false)
        #expect(try await a.device().writer == nil)

        _ = try await a.createSaveBackup(gameId: DemoLibrary.sample.anyGameWithSave, label: nil)
        #expect(try await a.hello().writerHeld)
        #expect(try await a.device().writer?.you == true)
        let seenByB = try #require(try await b.writer().writer)
        #expect(seenByB.clientName == "iPhone A")
        #expect(!seenByB.you)

        // B can read, not write; nothing changed.
        let trashBefore = try await b.trash().entries.count
        await #expect(throws: APIError.self) { _ = try await b.emptyTrash() }
        do {
            _ = try await b.emptyTrash()
        } catch let error as APIError {
            #expect(error.isReadOnly)
            #expect(error.readOnlyWriter == "iPhone A")
            if case .server(let server) = error { #expect(server.status == 423) }
        }
        #expect(try await b.trash().entries.count == trashBefore)
        // Without force, PUT /writer does not take it either.
        await #expect(throws: APIError.self) { _ = try await b.takeWriter() }
        #expect(try await a.writer().writer?.you == true)
    }

    @Test func requestsOfTheHolderRenewAndTheLeaseExpiresAfterSixtySeconds() async throws {
        let pak = makePak()
        let clock = MutableClock()
        pak.leaseClock = { clock.now }
        let (a, _) = try await namedClient(pak, name: "iPhone A")
        let (b, _) = try await namedClient(pak, name: "iPhone B")
        let taken = try #require(try await a.takeWriter().writer)
        #expect(taken.you)
        #expect(abs(taken.expiresAt.timeIntervalSince(clock.now) - 60) < 2)

        clock.advance(50)
        _ = try await a.systems() // any signed request renews
        clock.advance(50)
        #expect(try await b.writer().writer?.clientName == "iPhone A")
        clock.advance(11)
        #expect(try await b.writer().writer == nil)
        #expect(try await b.hello().writerHeld == false)
        #expect(try await b.takeWriter().writer?.you == true)
    }

    @Test func forceTakesOverAndReleaseFreesTheLease() async throws {
        let pak = makePak()
        let (a, _) = try await namedClient(pak, name: "iPhone A")
        let (b, _) = try await namedClient(pak, name: "iPhone B")
        _ = try await a.takeWriter()
        #expect(try await b.takeWriter(force: true).writer?.you == true)
        do {
            _ = try await a.emptyTrash()
            Issue.record("the former holder wrote")
        } catch let error as APIError {
            #expect(error.readOnlyWriter == "iPhone B")
        }
        // Releasing a lease held by another client changes nothing.
        try await a.releaseWriter()
        #expect(try await a.writer().writer?.clientName == "iPhone B")
        try await b.releaseWriter()
        #expect(try await a.writer().writer == nil)
    }

    @Test func aDisconnectedClientGets401UntilItConnects() async throws {
        let pak = makePak()
        let (a, idA) = try await namedClient(pak, name: "iPhone A")
        _ = try await a.takeWriter()
        pak.disconnect(clientId: idA)
        #expect(pak.writerClient == nil)
        do {
            _ = try await a.device()
            Issue.record("a disconnected client was served")
        } catch let error as APIError {
            #expect(error.isDisconnected)
        }
        try await a.connectClient()
        #expect(try await a.device().writer == nil) // connecting does not take the lease
        // No effect for a client that is not disconnected.
        try await a.connectClient()
    }

    @Test func aForgottenClientIsUnknown() async throws {
        let pak = makePak()
        let (a, idA) = try await namedClient(pak, name: "iPhone A")
        _ = try await a.takeWriter()
        pak.forget(clientId: idA)
        #expect(pak.writerClient == nil)
        await #expect(throws: APIError.server(ServerError(status: 401, code: .unknownClient, message: "Unknown client"))) {
            _ = try await a.device()
        }
    }

    @Test func aSimulatedOtherWriterKeepsTheLeaseUntilTakenOver() async throws {
        let pak = makePak()
        let clock = MutableClock()
        pak.leaseClock = { clock.now }
        pak.simulateOtherWriter(name: "Other iPhone")
        let (a, _) = try await namedClient(pak, name: "iPhone A")
        clock.advance(600)
        #expect(try await a.writer().writer?.clientName == "Other iPhone")
        #expect(try await a.takeWriter(force: true).writer?.you == true)
    }
}

extension DemoLibrary {
    /// The id of a demo game that has a save.
    var anyGameWithSave: String {
        let system = systems.first { $0.id == "gba" } ?? systems[0]
        let game = system.games.first { $0.hasSave } ?? system.games[0]
        return DemoLibrary.id(system.folder + "/" + game.path)
    }
}

// MARK: - Decoding

@Suite("Write lease decoding (M10)")
struct WriterDecodingTests {
    @Test func readOnlyCarriesTheHolderName() async throws {
        let client = stubClient { _, _ in
            .error(423, code: "read_only", message: "Another device has write access", details: ["writer": "iPhone de Thomas"])
        }
        do {
            _ = try await client.emptyTrash()
            Issue.record("no error")
        } catch let error as APIError {
            #expect(error.code == .readOnly)
            #expect(error.isReadOnly)
            #expect(error.readOnlyWriter == "iPhone de Thomas")
        }
    }

    @Test func readOnlyWithoutDetailsHasNoName() {
        let error = APIError.server(ServerError(status: 423, code: .readOnly, message: ""))
        #expect(error.isReadOnly)
        #expect(error.readOnlyWriter == nil)
        #expect(ErrorCode(rawValue: "read_only") == .readOnly)
        #expect(ErrorCode(rawValue: "disconnected") == .disconnected)
    }

    @Test func disconnectedIsDecoded() async throws {
        let client = stubClient { _, _ in .error(401, code: "disconnected", message: "Disconnected") }
        await #expect(throws: APIError.server(ServerError(status: 401, code: .disconnected, message: "Disconnected"))) {
            _ = try await client.device()
        }
    }

    @Test func theErrorObserverSeesEveryErrorAnswer() async throws {
        let pak = makePak()
        let seen = Locked<[ServerError]>([])
        let (other, _) = try await namedClient(pak, name: "Other")
        _ = try await other.takeWriter()
        let before = ContinuousClock.now
        let (client, _) = try await namedClient(pak, name: "Mine") { error, sentAt in
            #expect(sentAt >= before)
            seen.mutate { $0.append(error) }
        }
        _ = try? await client.emptyTrash()
        #expect(seen.value.map(\.code) == [.readOnly])
        #expect(seen.value.first?.writerName == "Other")
    }

    @Test func writerStateAndTheDeviceField() throws {
        let decoder = ProtocolJSON.decoder()
        let free = try decoder.decode(WriterState.self, from: Data(#"{"writer":null,"extra":1}"#.utf8))
        #expect(free.writer == nil)
        let held = try decoder.decode(WriterState.self, from: Data(
            #"{"writer":{"clientName":"iPhone de Thomas","you":false,"expiresAt":"2026-11-18T17:01:00Z","future":true}}"#.utf8
        ))
        #expect(held.writer?.clientName == "iPhone de Thomas")
        #expect(held.writer?.you == false)

        let base = #""deviceId":"5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0","deviceName":"Brick","model":"brick","platform":"tg5040","os":"nextui","osVersion":null,"pakVersion":"1.0.0","network":{"ipv4":"1.2.3.4","ssid":null},"battery":null,"storage":[],"uptimeSeconds":1,"connectedClients":[]"#
        let old = try decoder.decode(DeviceStatus.self, from: Data("{\(base)}".utf8))
        #expect(!old.hasWriterField)
        #expect(old.writer == nil)
        let none = try decoder.decode(DeviceStatus.self, from: Data("{\(base),\"writer\":null}".utf8))
        #expect(none.hasWriterField)
        #expect(none.writer == nil)
        let mine = try decoder.decode(DeviceStatus.self, from: Data(
            "{\(base),\"writer\":{\"clientName\":\"Me\",\"you\":true,\"expiresAt\":\"2026-11-18T17:01:00Z\"}}".utf8
        ))
        #expect(mine.writer?.you == true)
    }

    @Test func helloWithoutWriterHeldIsFree() throws {
        let json = #"{"deviceId":"5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0","deviceName":"Brick","model":"brick","platform":"tg5040","os":"nextui","osVersion":null,"pakVersion":"1.0.0","apiVersion":1,"minClientVersion":"1.0.0","serverTime":"2026-11-18T17:00:00Z","pairingOpen":true}"#
        let hello = try ProtocolJSON.decoder().decode(Hello.self, from: Data(json.utf8))
        #expect(!hello.writerHeld)
    }

    @Test func takeWriterSendsForce() async throws {
        let bodies = Locked<[String]>([])
        let client = stubClient { request, body in
            bodies.mutate { $0.append(String(decoding: body, as: UTF8.self)) }
            #expect(request.httpMethod == "PUT")
            return .json(200, ["writer": ["clientName": "Me", "you": true, "expiresAt": "2026-11-18T17:01:00Z"]])
        }
        _ = try await client.takeWriter(force: true)
        _ = try await client.takeWriter()
        #expect(bodies.value == [#"{"force":true}"#, #"{"force":false}"#])
    }
}

/// A value shared with `@Sendable` closures.
final class Locked<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: Value
    init(_ value: Value) { _value = value }
    var value: Value { lock.withLock { _value } }
    func mutate(_ change: (inout Value) -> Void) { lock.withLock { change(&_value) } }
}

// MARK: - Lease keeper

@MainActor
@Suite("Write lease keeper (M10)", .serialized)
struct WriterLeaseTests {
    struct Setup {
        let pak: FakePak
        let client: APIClient
        let clientId: String
        let sleeper: ManualSleeper
        let lease: WriterLease
        let busy: Locked<Bool>
    }

    func setUp(foreground: Bool = true, otherWriter: Bool = false) async throws -> Setup {
        let pak = makePak()
        if otherWriter { pak.simulateOtherWriter(name: "Other iPhone") }
        let (client, clientId) = try await namedClient(pak, name: "Mine")
        let sleeper = ManualSleeper()
        let busy = Locked(false)
        let lease = WriterLease(
            isForeground: foreground, sleep: { try await sleeper.sleep($0) },
            client: { client }, transfersRunning: { busy.value }
        )
        return Setup(pak: pak, client: client, clientId: clientId, sleeper: sleeper, lease: lease, busy: busy)
    }

    @Test func takesTheFreeLeaseAndRenewsEveryTwentySeconds() async throws {
        let s = try await setUp()
        s.lease.start(device: try await s.client.device())
        await waitFor { s.lease.access == .writer && s.sleeper.pending == 1 }
        #expect(s.lease.access == .writer)
        #expect(s.lease.expiresAt != nil)
        #expect(writerRequests(s.pak, "PUT") == 1)
        #expect(s.sleeper.requested == [.seconds(20)])
        for n in 2...4 {
            s.sleeper.fire()
            await waitFor { writerRequests(s.pak, "PUT") == n && s.sleeper.pending == 1 }
            #expect(writerRequests(s.pak, "PUT") == n)
        }
        #expect(s.sleeper.requested.allSatisfy { $0 == .seconds(20) })
        #expect(s.pak.writerClient == s.clientId)
        s.lease.stop()
    }

    @Test func doesNotStealALeaseHeldByAnotherClient() async throws {
        let s = try await setUp(otherWriter: true)
        let device = try await s.client.device()
        s.lease.start(device: device)
        #expect(s.lease.access == .readOnly(holder: "Other iPhone", tookOver: false))
        await waitFor { s.sleeper.pending == 1 }
        s.sleeper.fire()
        await waitFor { writerRequests(s.pak, "PUT") == 2 && s.sleeper.pending == 1 }
        #expect(s.lease.access == .readOnly(holder: "Other iPhone", tookOver: false))
        #expect(s.pak.writerClient != s.clientId)
        #expect(!s.pak.log.contains { $0.method == "PUT" && $0.path == "/api/v1/writer" && $0.status == 200 })
        s.lease.stop()
    }

    @Test func takesTheLeaseOnceItIsFreeAgain() async throws {
        let s = try await setUp()
        let (other, _) = try await namedClient(s.pak, name: "Other iPhone")
        _ = try await other.takeWriter()
        var acquired = 0
        s.lease.onAcquired = { acquired += 1 }
        s.lease.start(device: try await s.client.device())
        await waitFor { s.sleeper.pending == 1 }
        #expect(s.lease.isReadOnly)
        try await other.releaseWriter()
        s.sleeper.fire()
        await waitFor { s.lease.access == .writer }
        #expect(s.lease.access == .writer)
        #expect(acquired == 1)
        s.lease.stop()
    }

    @Test func nothingIsSentInTheBackground() async throws {
        let s = try await setUp(foreground: false)
        s.lease.start(device: try await s.client.device())
        try await Task.sleep(for: .milliseconds(100))
        #expect(writerRequests(s.pak, "PUT") == 0)
        #expect(!s.lease.isRunning)
        #expect(s.lease.access == .free)
    }

    @Test func releasesWhenGoingToTheBackgroundWithoutTransfers() async throws {
        let s = try await setUp()
        s.lease.start(device: try await s.client.device())
        await waitFor { s.lease.access == .writer }
        await s.lease.setForeground(false)
        #expect(writerRequests(s.pak, "DELETE") == 1)
        #expect(s.pak.writerClient == nil)
        #expect(!s.lease.isRunning)

        // Back in the foreground: taken again at once.
        await s.lease.setForeground(true)
        await waitFor { s.lease.access == .writer }
        #expect(s.pak.writerClient == s.clientId)
        s.lease.stop()
    }

    @Test func keepsRenewingInTheBackgroundWhileATransferRuns() async throws {
        let s = try await setUp()
        s.lease.start(device: try await s.client.device())
        await waitFor { s.lease.access == .writer && s.sleeper.pending == 1 }
        s.busy.mutate { $0 = true }
        await s.lease.setForeground(false)
        #expect(writerRequests(s.pak, "DELETE") == 0)
        s.sleeper.fire()
        await waitFor { writerRequests(s.pak, "PUT") == 2 && s.sleeper.pending == 1 }
        #expect(writerRequests(s.pak, "PUT") == 2)
        // The transfer ends: released at the next turn, then nothing.
        s.busy.mutate { $0 = false }
        s.sleeper.fire()
        await waitFor { writerRequests(s.pak, "DELETE") == 1 }
        #expect(s.pak.writerClient == nil)
        await waitFor { !s.lease.isRunning }
        #expect(!s.lease.isRunning)
    }

    @Test func aTakeOverByAnotherClientMakesThisOneReadOnly() async throws {
        let s = try await setUp()
        s.lease.start(device: try await s.client.device())
        await waitFor { s.lease.access == .writer && s.sleeper.pending == 1 }
        let (other, _) = try await namedClient(s.pak, name: "Other iPhone")
        _ = try await other.takeWriter(force: true)
        s.sleeper.fire()
        await waitFor { s.lease.isReadOnly }
        #expect(s.lease.access == .readOnly(holder: "Other iPhone", tookOver: true))
        s.lease.stop()
    }

    @Test func a423FromAWriteMakesItReadOnly() async throws {
        let s = try await setUp()
        s.lease.start(device: try await s.client.device())
        await waitFor { s.lease.access == .writer }
        s.lease.noteReadOnly(holder: "Other iPhone")
        #expect(s.lease.access == .readOnly(holder: "Other iPhone", tookOver: true))
        s.lease.noteReadOnly(holder: nil)
        #expect(s.lease.access == .readOnly(holder: "Other iPhone", tookOver: true))
        s.lease.stop()
    }

    @Test func takeOverForcesTheLease() async throws {
        let s = try await setUp(otherWriter: true)
        var acquired = 0
        s.lease.onAcquired = { acquired += 1 }
        s.lease.start(device: try await s.client.device())
        #expect(s.lease.isReadOnly)
        try await s.lease.takeOver()
        #expect(s.lease.access == .writer)
        #expect(s.pak.writerClient == s.clientId)
        #expect(acquired == 1)
        #expect(s.pak.log.contains { $0.method == "PUT" && $0.path == "/api/v1/writer" && $0.status == 200 })
        // Renewals go on, the first after the interval.
        await waitFor { s.sleeper.pending == 1 }
        #expect(s.sleeper.pending == 1)
        s.lease.stop()
    }

    @Test func aDisconnectionStopsTheRenewals() async throws {
        let s = try await setUp()
        var fatal: [APIError] = []
        s.lease.onFatalError = { fatal.append($0) }
        s.lease.start(device: try await s.client.device())
        await waitFor { s.lease.access == .writer && s.sleeper.pending == 1 }
        s.pak.disconnect(clientId: s.clientId)
        s.sleeper.fire()
        await waitFor { !fatal.isEmpty }
        #expect(fatal.first?.isDisconnected == true)
        await waitFor { !s.lease.isRunning }
        #expect(!s.lease.isRunning)
    }

    /// Review M10 #4: the pak grants the lease, the app goes to the
    /// background before the answer arrives: the lease is still released.
    @Test func goingToTheBackgroundDuringAnAcquisitionReleases() async throws {
        let s = try await setUp()
        s.pak.delayNextAnswer(method: "PUT", path: "/api/v1/writer", by: 0.4)
        s.lease.start(device: try await s.client.device())
        await waitFor { writerRequests(s.pak, "PUT") == 1 }
        #expect(s.pak.writerClient == s.clientId) // granted, answer in flight
        await s.lease.setForeground(false)
        try await Task.sleep(for: .milliseconds(700))
        #expect(s.pak.writerClient == nil)
        #expect(s.lease.access != .writer)
        #expect(writerRequests(s.pak, "DELETE") >= 1)
    }

    /// Review M10 #2: a renewal refused before a take-over, answered after
    /// it, changes nothing; nor does a 423 of a request sent before it.
    @Test func aStale423DoesNotUndoATakeOver() async throws {
        let s = try await setUp(otherWriter: true)
        s.pak.delayNextAnswer(method: "PUT", path: "/api/v1/writer", by: 0.4)
        s.lease.start(device: try await s.client.device())
        await waitFor { writerRequests(s.pak, "PUT") == 1 }
        let beforeTakeOver = ContinuousClock.now
        try await s.lease.takeOver()
        #expect(s.lease.access == .writer)
        s.lease.noteReadOnly(holder: "Other iPhone", sentAt: beforeTakeOver)
        #expect(s.lease.access == .writer)
        try await Task.sleep(for: .milliseconds(700)) // the late 423 arrives
        #expect(s.lease.access == .writer)
        #expect(s.pak.writerClient == s.clientId)
        // A 423 of a request sent after the take-over still counts.
        s.lease.noteReadOnly(holder: "Other iPhone", sentAt: .now)
        #expect(s.lease.isReadOnly)
        s.lease.stop()
    }

    func index(_ pak: FakePak, _ method: String, after start: Int = 0) -> Int? {
        pak.log.indices.first { $0 >= start && pak.log[$0].method == method && pak.log[$0].path == "/api/v1/writer" }
    }

    /// Review M10 round 2 #1: the renewal is still on its way to the pak
    /// (held at a gate) when the app goes to the background; the release
    /// waits for it, so the pak handles the DELETE last and the lease ends
    /// free.
    @Test func aReleaseWaitsForARenewalThatArrivesLate() async throws {
        let s = try await setUp()
        let gate = s.pak.holdNextArrival(method: "PUT", path: "/api/v1/writer")
        s.lease.start(device: try await s.client.device())
        await waitFor { gate.reached }
        #expect(gate.reached, "the PUT is pending")
        let background = Task { await s.lease.setForeground(false) }
        // A release that does not wait would reach the pak now.
        await waitFor(timeout: .milliseconds(300)) { index(s.pak, "DELETE") != nil }
        gate.open()
        await background.value
        await waitFor { index(s.pak, "DELETE") != nil }
        #expect(s.pak.writerClient == nil)
        let put = try #require(index(s.pak, "PUT"))
        let delete = try #require(index(s.pak, "DELETE"))
        #expect(put < delete, "the pak handled the PUT before the DELETE")
        #expect(s.lease.access != .writer)
    }

    /// Same for a take-over in flight.
    @Test func aReleaseWaitsForATakeOverThatArrivesLate() async throws {
        let s = try await setUp(otherWriter: true)
        s.lease.start(device: try await s.client.device())
        await waitFor { s.sleeper.pending == 1 }
        #expect(s.lease.isReadOnly)
        let before = s.pak.log.count
        let gate = s.pak.holdNextArrival(method: "PUT", path: "/api/v1/writer")
        let takeOver = Task { try await s.lease.takeOver() }
        await waitFor { gate.reached }
        #expect(gate.reached, "the take-over is pending")
        let background = Task { await s.lease.setForeground(false) }
        await waitFor(timeout: .milliseconds(300)) { index(s.pak, "DELETE", after: before) != nil }
        gate.open()
        try? await takeOver.value
        await background.value
        await waitFor { index(s.pak, "DELETE", after: before) != nil }
        #expect(s.pak.writerClient == nil, "taken over, then released")
        let put = try #require(index(s.pak, "PUT", after: before))
        let delete = try #require(index(s.pak, "DELETE", after: before))
        #expect(put < delete)
    }

    @Test func aPakWithoutTheLeaseIsUnsupported() async throws {
        let client = stubClient { _, _ in .error(404, code: "not_found", message: "No route") }
        let sleeper = ManualSleeper()
        let lease = WriterLease(isForeground: true, sleep: { try await sleeper.sleep($0) }, client: { client }, transfersRunning: { false })
        lease.start(device: DeviceStatus(
            deviceId: "d", deviceName: "Brick", model: .brick, platform: "tg5040", os: "nextui", osVersion: nil,
            pakVersion: "1.0.0", network: .init(ipv4: "1.2.3.4", ssid: nil), battery: nil, storage: [], uptimeSeconds: 1,
            hasWriterField: false
        ))
        await waitFor { lease.access == .unsupported }
        #expect(lease.access == .unsupported)
        #expect(!lease.isReadOnly)
        await waitFor { !lease.isRunning }
        #expect(!lease.isRunning)
    }
}

// MARK: - Uploads

@MainActor
@Suite("Uploads and the write lease (M10)")
struct UploadLeaseTests {
    @Test func anUploadRefusedWith423IsPausedThenResumes() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (other, _) = try await namedClient(pak, name: "Other iPhone")
        _ = try await other.takeWriter()
        let (client, _) = try await namedClient(pak, name: "Mine")
        let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-lease-\(UUID().uuidString)")
        let queue = TransferQueue(scratchDirectory: scratch, retry: .immediate) { client }
        let item = try queue.enqueue(fileURL: try makeFile(size: 50_000, name: "L.gba"),
                                     target: .rom(systemId: gbaSystemId, fileName: "L.gba"), destination: "GBA", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.phase == .paused)
        #expect((item.failure as? APIError)?.readOnlyWriter == "Other iPhone")
        #expect(queue.pausedForWriteAccess.count == 1)
        #expect(!queue.isBusy)

        try await other.releaseWriter()
        queue.resumePausedForWriteAccess()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
        #expect(item.sentBytes == 50_000)
    }

    @Test func theFormerHolderPausesMidUploadAndNeverLosesIt() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (client, _) = try await namedClient(pak, name: "Mine")
        let (other, _) = try await namedClient(pak, name: "Other iPhone")
        let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-lease-\(UUID().uuidString)")
        let queue = TransferQueue(scratchDirectory: scratch, retry: .immediate) { client }
        // Slow enough for the other client to take over in the middle.
        pak.responseDelay = 0.05
        let item = try queue.enqueue(fileURL: try makeFile(size: 200_000, name: "M.gba"),
                                     target: .rom(systemId: gbaSystemId, fileName: "M.gba"), destination: "GBA", ownsFile: false)
        await waitFor { item.sentBytes >= 32 * 1024 || item.phase.isFinished }
        _ = try await other.takeWriter(force: true)
        await queue.waitUntilIdle()
        #expect(item.phase == .paused)
        #expect((item.failure as? APIError)?.readOnlyWriter == "Other iPhone")
        #expect(item.sentBytes > 0)

        pak.responseDelay = 0
        try await other.releaseWriter()
        queue.resumePausedForWriteAccess()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
        // Resumed at the confirmed offset, not from 0.
        #expect(pak.chunkLog.filter { $0.offset == 0 }.count == 1)
    }

    /// Review M10 #5: a cancel refused by the console keeps the upload
    /// (local copy and resume state); it resumes with the lease.
    @Test func aCancelRefusedWith423KeepsTheUpload() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (client, _) = try await namedClient(pak, name: "Mine")
        let (other, _) = try await namedClient(pak, name: "Other iPhone")
        let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-lease-\(UUID().uuidString)")
        let queue = TransferQueue(scratchDirectory: scratch, retry: .immediate) { client }
        var pausedHook = 0
        queue.onPausedForWriteAccess = { _ in pausedHook += 1 }
        pak.responseDelay = 0.05
        let file = try makeFile(size: 200_000, name: "K.gba")
        let item = try queue.enqueue(fileURL: file, target: .rom(systemId: gbaSystemId, fileName: "K.gba"),
                                     destination: "GBA", ownsFile: true)
        await waitFor { item.sentBytes >= 32 * 1024 || item.phase.isFinished }
        _ = try await other.takeWriter(force: true)
        await queue.waitUntilIdle()
        #expect(item.phase == .paused)
        #expect(pausedHook == 1)
        pak.responseDelay = 0
        queue.cancel(item)
        await waitFor { item.phase != .cancelling }
        #expect(item.phase == .paused)
        #expect((item.failure as? APIError)?.isReadOnly == true)
        // Round 2 #3: the same pause path, so the session can resume it.
        #expect(pausedHook == 2)
        #expect(pak.uploadIds.count == 1)
        try await Task.sleep(for: .milliseconds(100))
        #expect(FileManager.default.fileExists(atPath: file.path), "the local copy is kept")

        try await other.releaseWriter()
        queue.resumePausedForWriteAccess()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
        #expect(pak.chunkLog.filter { $0.offset == 0 }.count == 1)
    }

    /// Round 2 #3: a cancel refused after `complete` was sent (answer lost)
    /// also goes through the pause path.
    @Test func aCancelRefusedAfterCompleteIsPausedThroughTheSamePath() async throws {
        let pak = makePak()
        let (client, _) = try await namedClient(pak, name: "Mine")
        let (other, _) = try await namedClient(pak, name: "Other iPhone")
        let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-lease-\(UUID().uuidString)")
        let queue = TransferQueue(scratchDirectory: scratch, retry: RetryPolicy(delays: [], sleep: { _ in })) { client }
        var pausedHook = 0
        queue.onPausedForWriteAccess = { _ in pausedHook += 1 }
        pak.loseCompleteRequests(1)
        let item = try queue.enqueue(fileURL: try makeFile(size: 10_000), target: .rom(systemId: gbaSystemId, fileName: "C2.gba"),
                                     destination: "", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.completeSent)
        _ = try await other.takeWriter(force: true)
        queue.cancel(item)
        await waitFor { pausedHook == 1 }
        #expect(pausedHook == 1)
        #expect(item.phase == .paused)
        #expect((item.failure as? APIError)?.isReadOnly == true)
        #expect(pak.uploadIds.count == 1)
    }

    /// Round 3 #2: while the cancel waits for the console, the upload is
    /// not finished: "Clear Finished" keeps it, and the refusal pauses it
    /// in the queue, where it resumes with the lease.
    @Test func anUploadBeingCancelledCannotBeCleared() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (client, _) = try await namedClient(pak, name: "Mine")
        let (other, _) = try await namedClient(pak, name: "Other iPhone")
        let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-lease-\(UUID().uuidString)")
        let queue = TransferQueue(scratchDirectory: scratch, retry: .immediate) { client }
        var pausedHook = 0
        queue.onPausedForWriteAccess = { _ in pausedHook += 1 }
        // The upload is created on the device, then the other iPhone takes
        // over before this one sends its first chunk.
        let created = pak.holdNextAnswer(method: "POST", path: "/api/v1/uploads")
        let file = try makeFile(size: 100_000, name: "Z.gba")
        let item = try queue.enqueue(fileURL: file, target: .rom(systemId: gbaSystemId, fileName: "Z.gba"),
                                     destination: "GBA", ownsFile: true)
        await waitFor { created.reached }
        _ = try await other.takeWriter(force: true)
        created.open()
        await queue.waitUntilIdle()
        #expect(item.phase == .paused)
        let delete = pak.holdNextAnswer(method: "DELETE", path: "/api/v1/uploads/\(pak.uploadIds[0])")
        queue.cancel(item)
        #expect(item.phase == .cancelling)
        await waitFor { delete.reached }
        #expect(delete.reached, "the DELETE was handled, its answer waits")
        queue.clearFinished()
        #expect(queue.uploads.contains { $0 === item }, "not finished: kept")
        #expect(FileManager.default.fileExists(atPath: file.path))
        delete.open()
        await waitFor { item.phase == .paused }
        #expect(item.phase == .paused)
        #expect(pausedHook >= 1)
        #expect(queue.uploads.contains { $0 === item })

        try await other.releaseWriter()
        queue.resumePausedForWriteAccess()
        await queue.waitUntilIdle()
        #expect(item.phase == .done)
    }

    /// Round 4 #1: a cancel that gets no answer (lost connection) is not a
    /// cancel: the upload, its id on the device and its local copy stay;
    /// it can be cancelled again (or resumed).
    @Test func aCancelWithoutAnswerKeepsTheUpload() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (client, _) = try await namedClient(pak, name: "Mine")
        let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-lease-\(UUID().uuidString)")
        let queue = TransferQueue(scratchDirectory: scratch, retry: RetryPolicy(delays: [], sleep: { _ in })) { client }
        pak.injectChunkFaults([.dropAfterStoring])
        let file = try makeFile(size: 100_000, name: "N.gba")
        let item = try queue.enqueue(fileURL: file, target: .rom(systemId: gbaSystemId, fileName: "N.gba"),
                                     destination: "GBA", ownsFile: true)
        await queue.waitUntilIdle()
        #expect(item.phase == .paused) // the connection dropped
        #expect(pak.uploadIds.count == 1)
        pak.enqueue(.networkFailure()) // the DELETE never reaches the pak
        queue.cancel(item)
        await waitFor { item.phase != .cancelling }
        #expect(item.phase == .failed)
        #expect(item.failure as? UploadFailure == .cancelNotConfirmed)
        #expect(pak.uploadIds.count == 1, "still on the device")
        try await Task.sleep(for: .milliseconds(100))
        #expect(FileManager.default.fileExists(atPath: file.path), "the local copy is kept")

        // Cancel again, now confirmed.
        queue.cancel(item)
        await waitFor { item.phase == .cancelled }
        #expect(item.phase == .cancelled)
        #expect(pak.uploadIds.isEmpty)
    }

    /// Round 4 #2: after a `complete` whose answer was lost, the cancel is
    /// `cancelling` too while the DELETE waits for its answer (no Resume
    /// meanwhile).
    @Test func aCancelAfterALostCompleteIsCancellingUntilAnswered() async throws {
        let pak = makePak()
        let (client, _) = try await namedClient(pak, name: "Mine")
        let scratch = FileManager.default.temporaryDirectory.appending(path: "rl-lease-\(UUID().uuidString)")
        let queue = TransferQueue(scratchDirectory: scratch, retry: RetryPolicy(delays: [], sleep: { _ in })) { client }
        pak.loseCompleteRequests(1)
        let item = try queue.enqueue(fileURL: try makeFile(size: 10_000), target: .rom(systemId: gbaSystemId, fileName: "C3.gba"),
                                     destination: "", ownsFile: false)
        await queue.waitUntilIdle()
        #expect(item.completeSent)
        let uploadId = try #require(pak.uploadIds.first)
        let delete = pak.holdNextAnswer(method: "DELETE", path: "/api/v1/uploads/\(uploadId)")
        queue.cancel(item)
        await waitFor { delete.reached }
        #expect(item.phase == .cancelling)
        queue.resume(item) // ignored while cancelling
        #expect(item.phase == .cancelling)
        delete.open()
        await waitFor { item.phase == .cancelled }
        #expect(item.phase == .cancelled)
    }
}
