import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// The access of the app to a console: the disconnection by the console
/// (M10, `POST /clients/me/connect` only on an explicit action), Forget,
/// and the writes run one at a time by the pak (M13: `429 Device busy`,
/// retried by itself for the end of an upload, said for a user action).
/// M13 removed the write lease, the read-only state and "Take Over".
@MainActor
@Suite("Console access (M10, M13)", .serialized)
struct ClientAccessAppTests {
    let secure = InMemorySecureStore()

    func makePak() -> FakePak {
        let pak = FakePak(host: "m13-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak) async throws -> (AppModel, String) {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: suite)),
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        try await app.pairDirectly(with: pak, clientName: "This iPhone")
        return (app, try app.store.identity().clientId)
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    func count(_ pak: FakePak, _ method: String, _ path: String) -> Int {
        pak.log.filter { $0.method == method && $0.path == "/api/v1" + path }.count
    }

    @Test func aDisconnectionIsShownAndOnlyAnExplicitActionReconnects() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        pak.disconnect(clientId: clientId)
        await session.refresh()
        #expect(session.isDisconnected)
        let error = try #require(session.error)
        #expect(error.action == .reconnect)
        #expect(error.message == String(localized: "Disconnected by the console."))

        // Automatic reconnections never lift it.
        await session.connect()
        #expect(session.isDisconnected)
        _ = try? await session.perform { try await $0.systems() }
        #expect(count(pak, "POST", "/clients/me/connect") == 0)
        #expect(pak.isDisconnected(clientId: clientId))

        // "Reconnect" does.
        await session.reconnect()
        #expect(session.state == .connected)
        #expect(count(pak, "POST", "/clients/me/connect") == 1)
        #expect(!pak.isDisconnected(clientId: clientId))

        // A later automatic reconnection does not call it again.
        pak.disconnect(clientId: clientId)
        await session.refresh()
        await session.connect()
        #expect(session.isDisconnected)
        #expect(count(pak, "POST", "/clients/me/connect") == 1)
    }

    @Test func openingTheConsoleFromTheListReconnects() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        pak.disconnect(clientId: clientId)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        #expect(session.isDisconnected)
        // Back to the list, then the user opens the console.
        app.path = []
        app.path = [.console(pak.deviceId)]
        await waitUntil { session.state == .connected }
        #expect(session.state == .connected)
        #expect(count(pak, "POST", "/clients/me/connect") == 1)
    }

    @Test func aForgottenIPhoneIsAskedToPairAgain() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        pak.forget(clientId: clientId)
        await session.refresh()
        let error = try #require(session.error)
        #expect(error.action == .repair)
        #expect(error.message == String(localized: "This console no longer recognizes your iPhone."))
        #expect(count(pak, "POST", "/clients/me/connect") == 0)
    }

    // MARK: - Codex review, round 1

    func picked(_ app: AppModel, name: String, contents: Data) async throws -> PickedFile {
        let directory = FileManager.default.temporaryDirectory.appending(path: "m10-src-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        let source = directory.appending(path: name)
        try contents.write(to: source)
        return try await PickedFile.copy(source, into: app.transferDirectory)
    }

    /// #1: a failed "Reconnect" leaves no permission behind: a later
    /// automatic attempt never sends `POST /clients/me/connect`.
    @Test func aFailedReconnectLeavesNoPermission() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        pak.disconnect(clientId: clientId)
        await session.refresh()
        #expect(session.isDisconnected)
        // "Reconnect" while the console cannot be reached: fails before /device.
        FakeURLProtocol.register(ClosureHandler { _, _ in .networkFailure() }, host: pak.host)
        await session.reconnect()
        #expect(!session.isDisconnected)
        pak.register()
        // An automatic attempt (an upload resuming, a retry...).
        await session.connect()
        _ = try? await session.perform { try await $0.systems() }
        #expect(session.isDisconnected)
        #expect(count(pak, "POST", "/clients/me/connect") == 0)
        #expect(pak.isDisconnected(clientId: clientId))
    }

    /// An upload refused by `401 disconnected` pauses (nothing lost) and
    /// goes on by itself once the user reconnects.
    @Test func anUploadPausedByADisconnectionResumesAfterReconnecting() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let system = try #require(try await session.perform { try await $0.systems() }.first)
        pak.disconnect(clientId: clientId)
        let file = FileManager.default.temporaryDirectory.appending(path: "m13-\(UUID().uuidString).gba")
        try Data(repeating: 3, count: 20_000).write(to: file)
        let item = try session.transfers.enqueue(
            fileURL: file, target: .rom(systemId: system.id, fileName: "Paused.gba"), destination: "GBA", ownsFile: false
        )
        await waitUntil { item.phase == .paused }
        #expect(item.phase == .paused)
        #expect((item.failure as? APIError)?.isDisconnected == true)
        await waitUntil { session.isDisconnected }
        await session.reconnect()
        await waitUntil { item.phase == .done }
        #expect(item.phase == .done, "\(String(describing: item.failure))")
    }

    /// The connection already ended by a `401 disconnected` answered to
    /// another request (a sync, a screen) before the upload sent anything:
    /// the upload pauses as well, never fails, and goes on after the user
    /// reconnects.
    @Test func anUploadAfterAnotherRequestWasDisconnectedPausesToo() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        app.syncOnConnect = false
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let system = try #require(try await session.perform { try await $0.systems() }.first)
        pak.disconnect(clientId: clientId)
        _ = try? await session.perform { try await $0.systems() }
        #expect(session.isDisconnected)
        let file = FileManager.default.temporaryDirectory.appending(path: "m13-\(UUID().uuidString).gba")
        try Data(repeating: 4, count: 20_000).write(to: file)
        let item = try session.transfers.enqueue(
            fileURL: file, target: .rom(systemId: system.id, fileName: "Late.gba"), destination: "GBA", ownsFile: false
        )
        await waitUntil { item.phase == .paused || item.phase == .failed }
        #expect(item.phase == .paused)
        #expect((item.failure as? APIError)?.isDisconnected == true)
        #expect(count(pak, "POST", "/uploads") == 0)
        await session.reconnect()
        await waitUntil { item.phase == .done }
        #expect(item.phase == .done, "\(String(describing: item.failure))")
    }

    /// The user reconnected before the refusal of the old connection
    /// reached the upload, and the `GET /device` check of the pause failed
    /// (a `500`): the upload goes on at the next request that succeeds.
    @Test func anUploadPausedAfterTheReconnectionGoesOn() async throws {
        let pak = makePak()
        pak.eventsSupported = false
        let (app, clientId) = try await pairedApp(pak)
        app.syncOnConnect = false
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let system = try #require(try await session.perform { try await $0.systems() }.first)
        let created = pak.holdNextAnswer(method: "POST", path: "/api/v1/uploads")
        pak.disconnect(clientId: clientId)
        let file = FileManager.default.temporaryDirectory.appending(path: "m13-\(UUID().uuidString).gba")
        try Data(repeating: 5, count: 20_000).write(to: file)
        let item = try session.transfers.enqueue(
            fileURL: file, target: .rom(systemId: system.id, fileName: "Early.gba"), destination: "GBA", ownsFile: false
        )
        // The refusal of `POST /uploads` waits; the user reconnects first.
        await waitUntil { created.reached }
        #expect(created.reached)
        await session.reconnect()
        #expect(!session.isDisconnected)
        #expect(item.phase == .uploading)
        pak.enqueue(.error(500, code: "internal")) // the check of the pause
        created.open()
        await waitUntil { item.phase == .paused }
        #expect(item.phase == .paused)
        try? await Task.sleep(for: .milliseconds(200))
        #expect(item.phase == .paused)
        _ = try await session.perform { try await $0.systems() }
        await waitUntil { item.phase == .done }
        #expect(item.phase == .done, "\(String(describing: item.failure))")
    }

    /// A cheat file refused by a disconnection waits, keeps its copy, and
    /// is imported once the user reconnects.
    @Test func aCheatFileWaitsForReconnection() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        pak.disconnect(clientId: clientId)
        let file = try await picked(app, name: "pack.cht", contents: Data("cheat0_desc = \"Wait\"\ncheat0_code = \"3\"\n".utf8))
        let game = DemoLibrary.id("Handheld Advance (GBA)/Pixel Runner.gba")
        let task = Task { try await session.importCheats(gameId: game, from: file, mode: .merge) }
        await waitUntil { session.reconnectionWaits == 1 }
        #expect(session.reconnectionWaits == 1)
        #expect(FileManager.default.fileExists(atPath: file.url.path))
        await session.reconnect()
        let result = try await task.value
        #expect(result.imported == 1)
    }

    /// The patch upload refused by a disconnection waits and goes on once
    /// the user reconnects (never restarted).
    @Test func aPatchUploadWaitsForReconnection() async throws {
        let pak = makePak()
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        pak.disconnect(clientId: clientId)
        let game = PatchGame(
            id: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest.gba"), name: "Sky Garden Quest",
            fileName: "Sky Garden Quest.gba", kind: .file, systemLabel: "Handheld Advance"
        )
        let flow = PatchFlow(session: session, game: game)
        flow.file = try await picked(app, name: "fix.bps", contents: DemoPatch.bps(sourceSize: 16_777_216, sourceCRC32: 1, target: Data(count: 64)))
        flow.fileName = "Sky Garden Quest (fix).gba"
        flow.start()
        await waitUntil { flow.isWaitingForReconnection }
        #expect(flow.isWaitingForReconnection)
        await session.reconnect()
        await waitUntil { flow.step == .patching || !flow.step.isBusy }
        #expect(flow.step == .patching || flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest (fix).gba"), late: false))
        // Refused (401, nothing created; or not sent at all, the session
        // already knowing it was disconnected), then created once.
        #expect(pak.log.filter { $0.method == "POST" && $0.path == "/api/v1/uploads" && $0.status == 401 }.count <= 1)
        #expect(pak.log.contains { $0.status == 401 })
        #expect(pak.log.filter { $0.method == "POST" && $0.path == "/api/v1/uploads" && $0.status < 300 }.count == 1)
    }

    // MARK: - Writes one at a time (M13)

    /// The end of an upload that finds another write running gets
    /// `429 Device busy`: nothing was written, the app sends it again
    /// after `Retry-After`, by itself.
    @Test func aBusyUploadEndIsSentAgainByItself() async throws {
        let pak = makePak()
        let (app, _) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let system = try #require(try await session.perform { try await $0.systems() }.first)
        pak.busyNextWrites(1, retryAfter: 1)
        let file = FileManager.default.temporaryDirectory.appending(path: "m13-\(UUID().uuidString).gba")
        try Data(repeating: 5, count: 20_000).write(to: file)
        let item = try session.transfers.enqueue(
            fileURL: file, target: .rom(systemId: system.id, fileName: "Busy.gba"), destination: "GBA", ownsFile: false
        )
        await waitUntil(timeout: .seconds(10)) { item.phase == .done || item.phase == .failed }
        #expect(item.phase == .done, "\(String(describing: item.failure))")
        #expect(pak.busyRefusals == 1)
        let completes = pak.log.filter { $0.method == "POST" && $0.path.hasSuffix("/complete") }.map(\.status)
        #expect(completes == [429, 200] || completes == [429, 201])
        #expect(session.state == .connected)
    }

    /// A user action that finds the console busy says so (nothing was
    /// changed, try again in N seconds); the connection stays.
    @Test func aBusyUserActionSaysSo() async throws {
        let pak = makePak()
        let (app, _) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let game = DemoLibrary.id("Handheld Advance (GBA)/Pixel Runner.gba")
        pak.busyNextWrites(1, retryAfter: 3)
        do {
            _ = try await session.deleteGame(id: game, keepSave: false)
            Issue.record("expected Device busy")
        } catch let error as AppError {
            #expect(error.isDeviceBusy)
            #expect(error.message == String(localized: "The console is busy with another change. Nothing was changed. Try again in \(3) seconds."))
            #expect(error.action == .retry)
        }
        #expect(session.state == .connected)
        #expect(pak.library.game(id: game) != nil, "nothing was deleted")
        // Tried again: done.
        _ = try await session.deleteGame(id: game, keepSave: false)
        #expect(pak.library.game(id: game) == nil)
    }

    /// A pak from M13 said "busy" with `429 too_many_attempts` and the
    /// message `Device busy` (no `device_busy` code yet): same text, same
    /// retry. Another `429 too_many_attempts` keeps its own text.
    @Test func aBusyAnswerOfAnOlderPakSaysTheSame() {
        let legacy = AppError(APIError.server(ServerError(status: 429, code: .tooManyAttempts, message: "Device busy", retryAfter: 3)))
        #expect(legacy.isDeviceBusy)
        #expect(legacy.message == String(localized: "The console is busy with another change. Nothing was changed. Try again in \(3) seconds."))
        let current = AppError(APIError.server(ServerError(status: 429, code: .deviceBusy, message: "Device busy")))
        #expect(current.isDeviceBusy)
        #expect(current.message == String(localized: "The console is busy with another change. Nothing was changed. Try again in a moment."))
        let other = AppError(APIError.server(ServerError(status: 429, code: .tooManyAttempts, message: "Too many requests", retryAfter: 3)))
        #expect(!other.isDeviceBusy)
        #expect(other.message == String(localized: "Too many attempts. Try again in \(3) seconds."))
    }

    /// The console disconnects this app during the patch upload, and the
    /// user cancels before the app learns it. While the DELETE waits for
    /// the console, the screen cannot be closed and the local copy stays
    /// (closing does nothing); refused, the upload stays, paused, and goes
    /// on once the user reconnects.
    @Test func aRefusedPatchCancelKeepsTheUploadPaused() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (app, clientId) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let game = PatchGame(
            id: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest.gba"), name: "Sky Garden Quest",
            fileName: "Sky Garden Quest.gba", kind: .file, systemLabel: "Handheld Advance"
        )
        let flow = PatchFlow(session: session, game: game)
        let file = try await picked(app, name: "big.bps", contents: DemoPatch.bps(sourceSize: 16_777_216, sourceCRC32: 1, target: Data(count: 300_000)))
        flow.file = file
        flow.fileName = "Sky Garden Quest (big).gba"
        // The upload is created, then its first chunk waits at the door
        // while the console disconnects this app.
        let chunkGate = Locked<FakeGate?>(nil)
        let created = pak.holdNextAnswer(method: "POST", path: "/api/v1/uploads")
        flow.start()
        await waitUntil { created.reached }
        let uploadId = try #require(pak.uploadIds.first)
        chunkGate.set(pak.holdNextArrival(method: "PUT", path: "/api/v1/uploads/\(uploadId)"))
        created.open()
        await waitUntil { chunkGate.value?.reached == true }
        pak.disconnect(clientId: clientId)
        #expect(!session.isDisconnected, "this iPhone does not know yet")

        let delete = pak.holdNextAnswer(method: "DELETE", path: "/api/v1/uploads/\(uploadId)")
        let cancel = Task { await flow.cancel() }
        await waitUntil { delete.reached }
        #expect(flow.step == .cancellingUpload)
        #expect(flow.step.holdsTheScreen, "the sheet cannot be dismissed")
        flow.discardFile() // closing the sheet now
        #expect(FileManager.default.fileExists(atPath: file.url.path), "the local copy stays during the DELETE")
        delete.open()
        await cancel.value
        chunkGate.value?.open()
        await waitUntil { flow.isWaitingForReconnection }
        #expect(flow.isWaitingForReconnection, "paused, not cancelled: \(flow.step)")
        #expect(FileManager.default.fileExists(atPath: file.url.path), "the local copy is kept")
        #expect(pak.uploadIds.count == 1)

        await session.reconnect()
        await waitUntil { flow.step == .patching || !flow.step.isBusy }
        #expect(flow.step == .patching || flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest (big).gba"), late: false))
        #expect(pak.log.filter { $0.method == "POST" && $0.path == "/api/v1/uploads" && $0.status < 300 }.count == 1)
    }

    /// Round 4 #1: a patch upload cancel without answer (lost connection)
    /// keeps the upload and its local copy: Resume or Cancel Again.
    @Test func aPatchCancelWithoutAnswerKeepsTheUpload() async throws {
        let pak = makePak()
        pak.chunkSize = 16 * 1024
        let (app, _) = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await session.connect()
        let game = PatchGame(
            id: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest.gba"), name: "Sky Garden Quest",
            fileName: "Sky Garden Quest.gba", kind: .file, systemLabel: "Handheld Advance"
        )
        let flow = PatchFlow(session: session, game: game)
        let file = try await picked(app, name: "net.bps", contents: DemoPatch.bps(sourceSize: 16_777_216, sourceCRC32: 1, target: Data(count: 100_000)))
        flow.file = file
        flow.fileName = "Sky Garden Quest (net).gba"
        let created = pak.holdNextAnswer(method: "POST", path: "/api/v1/uploads")
        flow.start()
        await waitUntil { created.reached }
        let uploadId = try #require(pak.uploadIds.first)
        let chunk = pak.holdNextArrival(method: "PUT", path: "/api/v1/uploads/\(uploadId)")
        created.open()
        await waitUntil { chunk.reached }
        pak.enqueue(.networkFailure()) // the DELETE never reaches the pak
        await flow.cancel()
        #expect(flow.step == .failed(.upload(.cancelNotConfirmed)))
        #expect(!flow.step.isBusy)
        #expect(FileManager.default.fileExists(atPath: file.url.path), "the local copy is kept")
        #expect(pak.uploadIds.count == 1)
        chunk.open()

        flow.start() // Resume
        await waitUntil { flow.step == .patching || !flow.step.isBusy }
        #expect(flow.step == .patching || flow.step == .succeeded(gameId: DemoLibrary.id("Handheld Advance (GBA)/Sky Garden Quest (net).gba"), late: false))
        #expect(pak.log.filter { $0.method == "POST" && $0.path == "/api/v1/uploads" && $0.status < 300 }.count == 1)
    }
}

/// A value shared with `@Sendable` closures.
final class Locked<Value>: @unchecked Sendable {
    private let lock = NSLock()
    private var _value: Value
    init(_ value: Value) { _value = value }
    var value: Value { lock.withLock { _value } }
    func set(_ value: Value) { lock.withLock { _value = value } }
}
