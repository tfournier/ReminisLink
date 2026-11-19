import CryptoKit
import Foundation
import ReminisLinkDemo
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Milestone M11 in the app: the tabs of an open console (one navigation
/// path for deep links), the download of a file of a game, and the return
/// to the list of consoles when the console closes ReminisLink.
@MainActor
@Suite("App ergonomics (M11)", .serialized)
struct ErgonomicsAppTests {
    let secure = InMemorySecureStore()
    static let skyGarden = DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest.gba")
    static let pixelRunner = DemoLibrary.id("Game Boy Advance (GBA)/Pixel Runner.gba")

    /// Sessions hold their app unowned, and connections started by opening
    /// a console may outlive a test: its app stays alive.
    static var kept: [AppModel] = []

    func makePak() -> FakePak {
        let pak = FakePak(host: "m11-\(UUID().uuidString.lowercased()).test")
        pak.register()
        return pak
    }

    func pairedApp(_ pak: FakePak) async throws -> AppModel {
        let suite = "ReminisLinkTests.\(UUID().uuidString)"
        let root = FileManager.default.temporaryDirectory.appending(path: suite)
        let settings = UserDefaults(suiteName: suite)!
        settings.set(false, forKey: AppModel.syncOnConnectKey)
        let app = AppModel(
            store: DeviceStore(secureStore: secure, defaults: UserDefaults(suiteName: suite)!),
            discovery: Discovery(fixedDevices: [pak.discoveredDevice]),
            artCache: ArtCache(directory: root.appending(path: "art")),
            exportDirectory: root.appending(path: "exports"),
            settings: settings,
            lookupTimeout: .milliseconds(300),
            makeSession: { APIClient.makeSession(protocolClasses: [FakeURLProtocol.self]) }
        )
        app.discovery.start()
        Self.kept.append(app)
        let clientId = try app.store.installationClientId()
        let attempt = try PairingAttempt(secret: .qr(pak.secret), deviceId: pak.deviceId, clientId: clientId, clientName: "This iPhone")
        let key = try await APIClient(endpoint: pak.endpoint, session: pak.makeSession()).pair(attempt)
        try app.store.save(deviceId: pak.deviceId, name: "Brick", model: .brick, clientId: clientId, key: key)
        return app
    }

    func waitUntil(timeout: Duration = .seconds(5), _ condition: () -> Bool) async {
        let deadline = ContinuousClock.now + timeout
        while !condition() && ContinuousClock.now < deadline {
            try? await Task.sleep(for: .milliseconds(20))
        }
    }

    // MARK: - Tabs

    @Test func thePathOpensTheTabOfItsSecondRoute() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let id = pak.deviceId
        let game = GameRoute(deviceId: id, gameId: Self.skyGarden, systemLabel: "Game Boy Advance", name: "Sky Garden Quest", hasArt: true)

        // As the Patch screen opens a new ROM.
        app.path = [.console(id), .library(id), .game(game)]
        #expect(app.openConsoleId == id)
        #expect(app.consoleTab == .library)
        #expect(app.tabPaths[.library] == [.game(game)])
        #expect(app.path == [.console(id), .library(id), .game(game)])
        #expect(app.listPath.isEmpty)

        // The game sheet goes back after a deletion.
        app.path.removeLast()
        #expect(app.consoleTab == .library)
        #expect(app.tabPaths[.library] == [])

        // As the Add screen after an upload: the Transfers tab.
        app.path = [.console(id), .transfers(id)]
        #expect(app.consoleTab == .transfers)
        #expect(app.path.last == .transfers(id))
        // The Library tab kept its screens (same console).
        app.consoleTab = .library
        #expect(app.path == [.console(id), .library(id)])

        // The dashboard: the Console tab.
        app.path = [.console(id)]
        #expect(app.consoleTab == .console)
        #expect(app.path == [.console(id)])

        // Back to the list.
        app.closeConsole()
        #expect(app.openConsoleId == nil)
        #expect(app.path.isEmpty)
        app.path = [.settings]
        #expect(app.listPath == [.settings])
        #expect(app.openConsoleId == nil)
        app.openConsole(id)
        #expect(app.openConsoleId == id)
        #expect(app.consoleTab == .console)
        #expect(app.tabPaths.values.allSatisfy { $0.isEmpty })
        #expect(app.listPath.isEmpty)
    }

    // MARK: - Downloads

    @Test func aFileOfAGameIsDownloadedForTheShareSheet() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let detail = try await session.perform { try await $0.game(id: Self.skyGarden) }
        let cheats = try #require(detail.files.first { $0.role == .cheats })
        let progress = FileDownload(fileId: try #require(cheats.fileId), expected: cheats.sizeBytes)
        let exported = try await session.downloadGameFile(gameId: Self.skyGarden, file: cheats, progress: progress)
        #expect(exported.file.url.lastPathComponent == "Sky Garden Quest.gba.cht")
        #expect(exported.file.directory.deletingLastPathComponent().standardizedFileURL == app.exportDirectory.standardizedFileURL)
        await waitUntil { progress.fraction == 1 }
        #expect(progress.fraction == 1)
        let size = try FileDigest.size(of: exported.file.url)
        #expect(progress.received == size)
        // The whole file, as announced (Codex M11 #4).
        #expect(size == cheats.sizeBytes)
    }

    @Test func aFileThatChangedIsReported() async throws {
        let pak = makePak()
        // Read-only does not matter: a download is a read.
        pak.simulateOtherWriter(name: "Other iPhone")
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        let detail = try await session.perform { try await $0.game(id: Self.skyGarden) }
        let save = try #require(detail.files.first { $0.role == .save })
        // Pixel Runner has no save: that file is not in its list.
        await #expect(throws: AppError.gameFileChanged) {
            try await session.downloadGameFile(
                gameId: Self.pixelRunner, file: save, progress: FileDownload(fileId: "f", expected: nil)
            )
        }
        #expect(AppError.gameFileChanged.action == nil)
        #expect(!AppError.gameFileChanged.message.isEmpty)
        let rom = try #require(detail.files.first { $0.role == .rom })
        let file = try await session.downloadGameFile(
            gameId: Self.skyGarden, file: rom, progress: FileDownload(fileId: "f", expected: nil)
        )
        let size = try FileDigest.size(of: file.file.url)
        #expect(size == rom.sizeBytes)
    }

    // MARK: - The console closes ReminisLink

    @Test func shuttingDownGoesBackToTheListAndReconnectsLater() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let id = pak.deviceId
        app.openConsole(id)
        let session = app.session(for: id)
        await waitUntil { session.state == .connected }
        #expect(session.state == .connected)
        app.consoleTab = .library

        pak.availability = .shuttingDown
        await #expect(throws: AppError.self) { try await session.perform { try await $0.allGames() } }
        #expect(app.openConsoleId == nil)
        #expect(app.path.isEmpty)
        #expect(app.closedNotice?.deviceId == id)
        #expect(session.state == .idle)
        #expect(session.closedByConsole)
        #expect(session.transfers.isHeldForClosedConsole)
        // No automatic connection meanwhile.
        await session.connectIfNeeded()
        #expect(session.state == .idle)

        // Later, the console is back and the user opens it again.
        pak.availability = .open
        app.openConsole(id)
        #expect(app.closedNotice == nil)
        await waitUntil { session.state == .connected }
        #expect(session.state == .connected)
        #expect(!session.closedByConsole)
        #expect(!session.transfers.isHeldForClosedConsole)
    }

    @Test func aShuttingDownSeenByAnyRequestCounts() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let id = pak.deviceId
        app.openConsole(id)
        let session = app.session(for: id)
        await waitUntil { session.state == .connected }
        let client = try #require(session.client)
        pak.availability = .shuttingDown
        // Not through `perform` (a transfer, the lease): the error observer.
        _ = try? await client.systems()
        await waitUntil { app.openConsoleId == nil }
        #expect(app.openConsoleId == nil)
        #expect(app.closedNotice?.deviceId == id)
    }

    @Test func aWiFiGlitchAloneStaysOnTheConsole() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.closureMinimumBrowsing = .zero
        let id = pak.deviceId
        app.openConsole(id)
        let session = app.session(for: id)
        await waitUntil { session.state == .connected }
        // The console is still announced: a connection failure is a glitch.
        pak.enqueue(.networkFailure(.networkConnectionLost))
        await #expect(throws: AppError.self) { try await session.perform { try await $0.systems() } }
        #expect(app.openConsoleId == id)
        #expect(app.closedNotice == nil)
        #expect(session.error != nil)
    }

    @Test func theServiceGoneAndAFailedConnectionGoBackToTheList() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.closureMinimumBrowsing = .zero
        let id = pak.deviceId
        app.openConsole(id)
        let session = app.session(for: id)
        await waitUntil { session.state == .connected }
        // The pak withdrew its announcement and quit.
        app.discovery.setFixedDevice(pak.discoveredDevice, visible: false)
        pak.availability = .gone
        await #expect(throws: AppError.self) { try await session.perform { try await $0.systems() } }
        #expect(app.openConsoleId == nil)
        #expect(app.closedNotice?.deviceId == id)
        #expect(session.state == .idle)
    }

    @Test func theServiceGoneRightAfterTheBrowserStartedIsNotEnough() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        // The default: Bonjour must have been browsing for a few seconds.
        let id = pak.deviceId
        app.openConsole(id)
        let session = app.session(for: id)
        await waitUntil { session.state == .connected }
        app.discovery.stop()
        app.discovery.start()
        app.discovery.setFixedDevice(pak.discoveredDevice, visible: false)
        pak.availability = .gone
        await #expect(throws: AppError.self) { try await session.perform { try await $0.systems() } }
        #expect(app.openConsoleId == id)
        #expect(app.closedNotice == nil)
    }

    /// Codex M11 #3: `503 shutting_down` while connecting (`/hello`) goes
    /// back to the list too, not to an error screen.
    @Test func shuttingDownWhileOpeningGoesBackToTheList() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let id = pak.deviceId
        pak.availability = .shuttingDown
        app.openConsole(id)
        let session = app.session(for: id)
        await waitUntil { app.openConsoleId == nil }
        #expect(app.openConsoleId == nil)
        #expect(app.closedNotice?.deviceId == id)
        #expect(session.state == .idle)
        #expect(session.error == nil)
        #expect(session.closedByConsole)
    }

    /// Codex M11 #2: once the console closed ReminisLink, nothing connects
    /// without the user: not a screen asking for a client, not an upload
    /// added by an Add sheet still copying, not the end of that sheet.
    @Test func aClosedConsoleNeverReconnectsByItself() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let id = pak.deviceId
        app.openConsole(id)
        let session = app.session(for: id)
        await waitUntil { session.state == .connected }
        pak.availability = .shuttingDown
        _ = try? await session.perform { try await $0.systems() }
        #expect(app.openConsoleId == nil)
        // The console is back, but the user has not opened it.
        pak.availability = .open
        let hellos = pak.log.filter { $0.path == "/api/v1/hello" }.count
        await #expect(throws: AppError.consoleClosed) { _ = try await session.connectedClient() }
        await #expect(throws: AppError.consoleClosed) { try await session.perform { try await $0.systems() } }
        await session.connect()
        // The end of an Add sheet: no console reopened.
        let file = FileManager.default.temporaryDirectory.appending(path: "m11-\(UUID().uuidString).gba")
        try Data(count: 1000).write(to: file)
        let item = try session.transfers.enqueue(
            fileURL: file, target: .rom(systemId: DemoLibrary.id("Game Boy Advance (GBA)"), fileName: "M11.gba"),
            destination: "GBA", ownsFile: false
        )
        #expect(item.phase == .paused)
        app.showTransfersAfterAdd(deviceId: id)
        #expect(app.openConsoleId == nil)
        #expect(app.closedNotice?.deviceId == id)
        try await Task.sleep(for: .milliseconds(200))
        #expect(pak.log.filter { $0.path == "/api/v1/hello" }.count == hellos)
        #expect(session.state == .idle)

        // The user opens it: connected, and the upload goes on.
        app.openConsole(id)
        await waitUntil { item.phase == .done }
        #expect(session.state == .connected)
        #expect(item.phase == .done)
        // A later Add ends on the Transfers tab, as before.
        app.showTransfersAfterAdd(deviceId: id)
        #expect(app.path == [.console(id), .transfers(id)])
    }

    /// Codex M11 #1: a pak older than M11 (no `GET /games`, files without
    /// `fileId`): per-system browsing, and the game sheet still loads.
    @Test func aPakOlderThanM11KeepsTheGameSheet() async throws {
        let pak = makePak()
        pak.allGamesSupported = false
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        await #expect {
            _ = try await session.perform { try await $0.allGames() }
        } throws: { error in
            guard case .api(let api)? = error as? AppError else { return false }
            return LibraryMode.meansUnsupported(api)
        }
        let detail = try await session.gameDetail(id: Self.skyGarden)
        #expect(detail.files.allSatisfy { $0.fileId == nil })
        #expect(detail.hasSave && detail.stateCount > 0 && detail.hasCheats)
        // The connection is not lost by the 404.
        #expect(session.state == .connected)
    }

    /// Codex M11 #5: the game itself is gone: said as such.
    @Test func aDeletedGameIsSaidGone() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        let session = app.session(for: pak.deviceId)
        _ = try await session.gameDetail(id: Self.pixelRunner)
        _ = try await session.deleteGame(id: Self.pixelRunner, keepSave: false)
        await #expect(throws: AppError.gameRemoved) { _ = try await session.gameDetail(id: Self.pixelRunner) }
        #expect(AppError.gameRemoved.action == nil)
    }

    @Test func aClosedConsoleThatIsNotOpenStaysOnTheList() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.path = [.settings]
        app.consoleClosed(deviceId: pak.deviceId)
        #expect(app.closedNotice == nil)
        #expect(app.listPath == [.settings])
    }
}
