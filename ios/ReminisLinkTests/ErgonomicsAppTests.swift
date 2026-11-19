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
        let progress = FileDownload(fileId: cheats.fileId, expected: cheats.sizeBytes)
        let exported = try await session.downloadGameFile(gameId: Self.skyGarden, file: cheats, progress: progress)
        #expect(exported.file.url.lastPathComponent == "Sky Garden Quest.gba.cht")
        #expect(exported.file.directory.deletingLastPathComponent().standardizedFileURL == app.exportDirectory.standardizedFileURL)
        await waitUntil { progress.fraction == 1 }
        #expect(progress.fraction == 1)
        let size = try FileDigest.size(of: exported.file.url)
        #expect(progress.received == size)
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
                gameId: Self.pixelRunner, file: save, progress: FileDownload(fileId: save.fileId, expected: nil)
            )
        }
        #expect(AppError.gameFileChanged.action == nil)
        #expect(!AppError.gameFileChanged.message.isEmpty)
        let rom = try #require(detail.files.first { $0.role == .rom })
        let file = try await session.downloadGameFile(
            gameId: Self.skyGarden, file: rom, progress: FileDownload(fileId: rom.fileId, expected: nil)
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

    @Test func aClosedConsoleThatIsNotOpenStaysOnTheList() async throws {
        let pak = makePak()
        let app = try await pairedApp(pak)
        app.path = [.settings]
        app.consoleClosed(deviceId: pak.deviceId)
        #expect(app.closedNotice == nil)
        #expect(app.listPath == [.settings])
    }
}
