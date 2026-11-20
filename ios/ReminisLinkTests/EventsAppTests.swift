import CryptoKit
import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M14 in the app: the event stream of the open console (closing
/// of the pak, lost connection, library, jobs, dashboard, older pak).
@MainActor
@Suite("Event stream in the app (M14)", .serialized)
struct EventsAppTests {
    let secure = InMemorySecureStore()
    static let skyGarden = DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest.gba")
    static let skyGardenGame = PatchGame(
        id: skyGarden, name: "Sky Garden Quest", fileName: "Sky Garden Quest.gba", kind: .file,
        systemLabel: "Handheld Advance"
    )

    /// Sessions hold their app unowned: the apps outlive the tests.
    static var kept: [AppModel] = []

    func makePak() -> FakePak {
        let pak = FakePak(host: "m14-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    /// A paired app, active (the stream runs in the foreground only).
    func pairedApp(_ pak: FakePak) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let root = FileManager.default.temporaryDirectory.appending(path: suite)
        let settings = UserDefaults(suiteName: suite)!
        settings.set(false, forKey: AppModel.syncOnConnectKey)
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            shareInbox: ShareInbox(root: root.appending(path: "inbox")),
            transferDirectory: root.appending(path: "transfers"),
            exportDirectory: root.appending(path: "exports"),
            settings: settings,
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        app.libraryEventDebounce = .milliseconds(50)
        Self.kept.append(app)
        try await app.pairDirectly(with: pak, clientName: "This iPhone")
        app.becomeActive()
        return app
    }

    /// Opens the console and waits for its stream to be live and caught up.
    func openLive(_ app: AppModel, _ pak: FakePak) async -> ConsoleSession {
        app.openConsole(pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await waitUntil { session.eventsLive && session.catchUpsDone >= 1 }
        #expect(session.state == .connected)
        #expect(session.eventsLive)
        return session
    }

    /// Another paired device, with its own identity (M13: several clients
    /// may write, one write at a time).
    func otherClient(_ pak: FakePak) -> APIClient {
        let identity = ClientIdentity.generate()
        pak.addClient(identity, name: "Other iPhone")
        return APIClient(
            endpoint: pak.endpoint, credentials: DeviceCredentials(deviceId: pak.deviceId, identity: identity, pin: pak.pin),
            session: pak.makeSession()
        )
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(10))
        }
    }

    func count(_ pak: FakePak, _ method: String, _ path: String) -> Int {
        pak.log.filter { $0.method == method && $0.path == APIClient.apiPrefix + path }.count
    }

    // MARK: - Closing

    @Test func shutdownGoesBackToTheListAtOnceWithoutAProbe() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        let hellos = count(pak, "GET", "/hello")
        let start = ContinuousClock.now
        pak.quit()
        await waitUntil { app.openConsoleId == nil }
        #expect(ContinuousClock.now - start < .seconds(2))
        #expect(app.path.isEmpty)
        #expect(app.closedNotice?.deviceId == pak.deviceId)
        #expect(session.closedByConsole)
        #expect(session.transfers.isHeldForClosedConsole)
        #expect(!session.eventsLive)
        // The event was enough: no `/hello` probe.
        #expect(count(pak, "GET", "/hello") == hellos)
    }

    @Test func anUnannouncedEndIsProbedThenTheStreamReconnects() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        let hellos = count(pak, "GET", "/hello")
        #expect(count(pak, "GET", "/events") == 1)
        pak.dropEventStreams()
        await waitUntil { count(pak, "GET", "/events") == 2 && session.eventsLive }
        #expect(count(pak, "GET", "/hello") == hellos + 1)
        #expect(session.eventsLive)
        #expect(session.state == .connected)
        #expect(app.openConsoleId == pak.deviceId)
        #expect(app.closedNotice == nil)
    }

    @Test func anUnannouncedEndOfAPakThatIsGoneMeansClosed() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        let hellos = count(pak, "GET", "/hello")
        pak.vanish()
        await waitUntil { app.openConsoleId == nil }
        #expect(app.closedNotice?.deviceId == pak.deviceId)
        #expect(session.closedByConsole)
        // The probe was sent, and failed to connect.
        #expect(count(pak, "GET", "/hello") == hellos + 1)
        #expect(pak.log.last { $0.path.hasSuffix("/hello") }?.status == 0)
    }

    @Test func silenceIsALostConnectionNotAClosedConsole() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        var timing = EventStreamTiming.live
        timing.livenessTimeout = .milliseconds(400)
        app.eventTiming = timing
        let session = await openLive(app, pak)
        pak.keepAlivesPaused = true
        await waitUntil { session.error != nil }
        #expect(session.error == .api(.network(.timedOut)))
        #expect(!session.eventsLive)
        #expect(app.openConsoleId == pak.deviceId)
        #expect(app.closedNotice == nil)
        #expect(!session.closedByConsole)
    }

    // MARK: - Library, jobs, dashboard

    @Test func aChangeMadeByAnotherDeviceRefreshesTheLibrary() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        let systemId = try #require(pak.library.game(id: Self.skyGarden)?.0.id)
        let revision = session.libraryRevision
        _ = try await otherClient(pak).deleteGame(id: Self.skyGarden, keepSave: false)
        await waitUntil { session.libraryRevision > revision }
        #expect(session.libraryRevision == revision + 1)
        #expect(session.lastLibraryChange == [systemId])
        #expect(session.libraryChangeAffects(systemId))
        #expect(!session.libraryChangeAffects("another-system"))
        // The flat library, reloaded, no longer has the game.
        let games = try await session.perform { try await $0.allGames() }
        #expect(!games.games.contains { $0.id == Self.skyGarden })
    }

    @Test func jobEventsDriveThePatchWithoutPolling() async throws {
        let pak = makePak()
        pak.jobTickInterval = 0.05
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        #expect(session.transfers.jobsFollowedByEvents)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: Self.skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        // Nobody calls `tick()`: the events move the flow on.
        await waitUntil(timeout: .seconds(10)) {
            if case .succeeded = flow.step { return true }
            if case .failed = flow.step { return true }
            return false
        }
        #expect(flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest (patched).gba"), late: false))
        #expect(session.transfers.jobs.map(\.kind) == [.patch])
        #expect(session.transfers.jobs.allSatisfy { $0.job?.state == .succeeded })
        // The screens' polling sends nothing while the stream is live.
        await flow.tick()
        await session.transfers.refreshJobs()
        #expect(!pak.log.contains { $0.method == "GET" && $0.path.hasPrefix(APIClient.apiPrefix + "/jobs") })
    }

    @Test func aPakWithoutEventsKeepsThePolling() async throws {
        let pak = makePak()
        pak.eventsSupported = false
        let app = try await pairedApp(pak)
        app.openConsole(pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await waitUntil { session.eventsUnsupported }
        #expect(session.eventsUnsupported)
        #expect(!session.eventsLive)
        #expect(!session.transfers.jobsFollowedByEvents)
        #expect(session.state == .connected)
        let files = try await session.perform { try await $0.sdFiles(type: .patch) }
        let flow = PatchFlow(session: session, game: Self.skyGardenGame)
        flow.source = .sdCard
        flow.sdFileId = files.first { $0.fileName == "Bonus Levels.ips" }?.id
        flow.start()
        for _ in 0..<500 {
            switch flow.step {
            case .patching: await flow.tick()
            case .succeeded, .failed, .cancelled: break
            default: try? await Task.sleep(for: .milliseconds(10))
            }
            if case .succeeded = flow.step { break }
            if case .failed = flow.step { break }
        }
        #expect(flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest (patched).gba"), late: false))
        #expect(pak.log.contains { $0.method == "GET" && $0.path.hasPrefix(APIClient.apiPrefix + "/jobs/") })
        // Tried once for this session, never again.
        app.enterBackground()
        app.becomeActive()
        try? await Task.sleep(for: .milliseconds(100))
        #expect(count(pak, "GET", "/events") == 1)
    }

    @Test func aDeviceEventUpdatesTheDashboard() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        pak.deviceEventInterval = 0
        let session = await openLive(app, pak)
        // `GET /device` read once after `hello`.
        await waitUntil { count(pak, "GET", "/device") >= 2 }
        #expect(count(pak, "GET", "/device") == 2)
        #expect(session.status?.battery?.percent == 78)
        pak.setBattery(percent: 42, charging: true)
        await waitUntil { session.status?.battery?.percent == 42 }
        #expect(session.status?.battery == DeviceStatus.Battery(percent: 42, charging: true))
        #expect(session.status?.connectedClients.map(\.clientName) == ["This iPhone"])
        // Not read again: the event was enough.
        #expect(count(pak, "GET", "/device") == 2)
    }

    @Test func theStreamStopsInTheBackgroundAndComesBack() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        app.enterBackground()
        #expect(!session.eventsLive)
        #expect(!session.transfers.jobsFollowedByEvents)
        await waitUntil { pak.eventStreamCount() == 0 }
        #expect(pak.eventStreamCount() == 0)
        app.becomeActive()
        await waitUntil { session.eventsLive }
        #expect(session.eventsLive)
        #expect(pak.eventStreamCount() == 1)
    }

    @Test func aDisconnectionFromTheConsoleEndsTheConnection() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        let clientId = try #require(pak.clientIds.first)
        pak.disconnect(clientId: clientId)
        // The stream ends without an event; the probe answers; the new
        // stream gets `401 disconnected`.
        await waitUntil { session.isDisconnected }
        #expect(session.isDisconnected)
        #expect(!session.eventsLive)
        #expect(app.closedNotice == nil)
    }

    // MARK: - Review round 1

    @Test func onlyTheConsoleShownKeepsAStream() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        #expect(pak.eventStreamCount() == 1)
        app.closeConsole()
        await waitUntil { pak.eventStreamCount() == 0 }
        #expect(pak.eventStreamCount() == 0)
        #expect(!session.eventsLive)
        // Back to the foreground on the list: no stream either.
        app.enterBackground()
        app.becomeActive()
        try? await Task.sleep(for: .milliseconds(100))
        #expect(pak.eventStreamCount() == 0)
        // Opened again: a stream again.
        app.openConsole(pak.deviceId)
        await waitUntil { session.eventsLive && pak.eventStreamCount() == 1 }
        #expect(pak.eventStreamCount() == 1)
    }

    @Test func changesMissedInTheBackgroundAreCaughtUp() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        app.enterBackground()
        await waitUntil { pak.eventStreamCount() == 0 }
        // No stream: the pak replays nothing of this.
        _ = try await otherClient(pak).deleteGame(id: Self.skyGarden, keepSave: false)
        let revision = session.libraryRevision
        app.becomeActive()
        await waitUntil { session.libraryRevision > revision }
        #expect(session.libraryRevision > revision)
        #expect(session.lastLibraryChange == nil)
        #expect(session.libraryChangeAffects("any-system"))
    }

    @Test func aDeviceAnswerOlderThanAnEventKeepsTheEvent() async throws {
        let pak = makePak()
        pak.deviceEventInterval = 0
        // The connection's `GET /device` goes; the one after `hello` waits.
        pak.holdNextAnswer(method: "GET", path: APIClient.apiPrefix + "/device").open()
        let held = pak.holdNextAnswer(method: "GET", path: APIClient.apiPrefix + "/device")
        let app = try await pairedApp(pak)
        app.openConsole(pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await waitUntil { held.reached }
        #expect(held.reached)
        pak.setBattery(percent: 42, charging: true)
        await waitUntil { session.status?.battery?.percent == 42 }
        // The held answer says 78 %: older than the event.
        held.open()
        await waitUntil { session.status?.battery?.percent != 42 }
        try? await Task.sleep(for: .milliseconds(100))
        #expect(session.status?.battery == DeviceStatus.Battery(percent: 42, charging: true))
    }

    // MARK: - Review round 2

    @Test func aChangeBeforeTheFirstHelloIsCaughtUp() async throws {
        let pak = makePak()
        // The first stream is slow to open: no `hello` before the app goes
        // to the background.
        let slow = pak.holdNextArrival(method: "GET", path: APIClient.apiPrefix + "/events")
        let app = try await pairedApp(pak)
        app.openConsole(pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await waitUntil { slow.reached }
        #expect(session.state == .connected)
        app.enterBackground()
        _ = try await otherClient(pak).deleteGame(id: Self.skyGarden, keepSave: false)
        let revision = session.libraryRevision
        app.becomeActive()
        // The first `hello` of the session reloads too.
        await waitUntil { session.catchUpsDone >= 1 }
        #expect(session.libraryRevision > revision)
        #expect(session.lastLibraryChange == nil)
        slow.open()
    }

    @Test func anOlderCatchUpNeverOverwritesANewerOne() async throws {
        let pak = makePak()
        pak.deviceEventInterval = 3600
        // The connection's `GET /device` goes; the first catch-up's waits.
        pak.holdNextAnswer(method: "GET", path: APIClient.apiPrefix + "/device").open()
        let firstCatchUp = pak.holdNextAnswer(method: "GET", path: APIClient.apiPrefix + "/device")
        let app = try await pairedApp(pak)
        app.openConsole(pak.deviceId)
        let session = app.session(for: pak.deviceId)
        await waitUntil { firstCatchUp.reached }
        // The stream ends (no `shutdown`): the probe answers, a new stream
        // and its catch-up follow. Meanwhile, with no stream open, the
        // battery changes without an event.
        pak.dropEventStreams()
        pak.setBattery(percent: 60, charging: true)
        await waitUntil { session.catchUpsDone >= 1 }
        #expect(session.status?.battery?.percent == 60)
        // The first catch-up's answer (78 %) arrives last.
        firstCatchUp.open()
        try? await Task.sleep(for: .milliseconds(200))
        #expect(session.status?.battery == DeviceStatus.Battery(percent: 60, charging: true))
    }

    @Test func onlyTheLastLoadIsShown() {
        var loads = LoadGeneration()
        let first = loads.begin()
        let second = loads.begin()
        #expect(!loads.isCurrent(first))
        #expect(loads.isCurrent(second))
    }

    // MARK: - Review round 3

    @Test func anOlderTrashAnswerNeverReshowsADeletedEntry() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = await openLive(app, pak)
        let entry = try #require(session.trash?.entries.first)
        // A load of the trash whose answer (with the entry) is held.
        let gate = pak.holdNextAnswer(method: "GET", path: APIClient.apiPrefix + "/trash")
        let older = Task { await session.loadTrash() }
        await waitUntil { gate.reached }
        // The entry is deleted for good; the trash is loaded again.
        try await session.deleteTrashEntry(entry.id)
        #expect(session.trash?.entries.contains { $0.id == entry.id } == false)
        gate.open()
        #expect(await older.value == false)
        #expect(session.trash?.entries.contains { $0.id == entry.id } == false)
    }
}
