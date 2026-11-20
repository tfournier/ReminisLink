import CryptoKit
import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// A console answered from memory: saves by game id, with failures to inject.
final class StubSaveSource: SaveSyncSource, @unchecked Sendable {
    struct Entry {
        var summary: SaveSummary
        var save: Data
        var rtc: Data?
        /// Served instead of `save` (a transfer that went wrong).
        var served: Data?
        var failure: APIError?

        static func entry(_ gameId: String, name: String, save: String, rtc: String? = nil) -> Entry {
            let data = Data(save.utf8)
            let clock = rtc.map { Data($0.utf8) }
            return Entry(
                summary: SaveSummary(
                    gameId: gameId, systemId: "sys-gba", gameName: name, fileName: "\(name).gba",
                    path: "Saves/GBA/\(name).gba.sav", sizeBytes: Int64(data.count), modifiedAt: Date(timeIntervalSince1970: 1_790_000_000),
                    sha256: sha256Hex(data),
                    rtc: clock.map { SaveClockFile(path: "Saves/GBA/\(name).gba.rtc", sizeBytes: Int64($0.count), modifiedAt: Date(), sha256: sha256Hex($0)) }
                ),
                save: data, rtc: clock
            )
        }
    }

    private let lock = NSLock()
    private var entries: [Entry]
    private(set) var downloads: [String] = []
    var systemsFail = false

    init(_ entries: [Entry]) { self.entries = entries }

    func update(_ gameId: String, _ change: (inout Entry) -> Void) {
        lock.withLock {
            guard let i = entries.firstIndex(where: { $0.summary.gameId == gameId }) else { return }
            change(&entries[i])
        }
    }

    func saves() async throws -> SaveList {
        lock.withLock { SaveList(saves: entries.map(\.summary), truncated: false, generatedAt: Date()) }
    }

    func systems() async throws -> [GameSystem] {
        if systemsFail { throw APIError.server(ServerError(status: 500, code: .internal, message: "busy")) }
        return [GameSystem(id: "sys-gba", label: "Game Boy Advance", folder: "Game Boy Advance (GBA)", tag: "GBA", gameCount: 3)]
    }

    func downloadSave(gameId: String, into directory: URL) async throws -> DownloadedFile {
        let entry = try find(gameId)
        lock.withLock { downloads.append(gameId) }
        if let failure = entry.failure { throw failure }
        return try write(entry.served ?? entry.save, name: "\(entry.summary.fileName).sav", into: directory)
    }

    func downloadSaveClock(gameId: String, into directory: URL) async throws -> DownloadedFile {
        let entry = try find(gameId)
        guard let rtc = entry.rtc else { throw APIError.server(ServerError(status: 404, code: .notFound, message: "No clock file")) }
        return try write(rtc, name: "\(entry.summary.fileName).rtc", into: directory)
    }

    private func find(_ gameId: String) throws -> Entry {
        guard let entry = lock.withLock({ entries.first { $0.summary.gameId == gameId } }) else {
            throw APIError.server(ServerError(status: 404, code: .notFound, message: "Game not found"))
        }
        return entry
    }

    private func write(_ data: Data, name: String, into directory: URL) throws -> DownloadedFile {
        let folder = directory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let url = folder.appending(path: name)
        try data.write(to: url)
        return DownloadedFile(url: url)
    }
}

/// Records the progress reports of a sync.
final class ProgressLog: @unchecked Sendable {
    private let lock = NSLock()
    private var _reports: [SaveSyncProgress] = []
    var reports: [SaveSyncProgress] { lock.withLock { _reports } }
    func add(_ p: SaveSyncProgress) { lock.withLock { _reports.append(p) } }
}

@Suite("Save sync (M9)")
struct SaveSyncTests {
    func makeSync(_ source: any SaveSyncSource, _ sandbox: borrowing VaultSandbox, clock: TestClock = TestClock()) -> SaveSync {
        SaveSync(console: brick, source: source, vault: sandbox.vault(clock: clock), scratchDirectory: sandbox.base.appending(path: "Scratch"))
    }

    @Test func aFirstSyncCopiesEverySaveThenOnlyWhatChanged() async throws {
        let sandbox = try VaultSandbox()
        let source = StubSaveSource([
            .entry("g1", name: "Sky Garden", save: "sky-1", rtc: "clock-1"),
            .entry("g2", name: "Mosslight Maze", save: "moon-1"),
        ])
        let clock = TestClock()
        let log = ProgressLog()
        let first = try await makeSync(source, sandbox, clock: clock).run { log.add($0) }
        #expect(first.stored == ["g1", "g2"])
        #expect(first.unchanged == 0 && first.failures.isEmpty && first.total == 2)
        #expect(first.location == .local)
        #expect(log.reports.last == SaveSyncProgress(total: 2, checked: 2, stored: 2))
        #expect(log.reports.map(\.checked) == log.reports.map(\.checked).sorted())

        let folder = sandbox.root.appending(path: "My Brick/Game Boy Advance (GBA)/Sky Garden")
        let versions = try await sandbox.vault().versions(deviceId: "dev-1", gameId: "g1")
        #expect(versions.count == 1)
        #expect(versions[0].info.rtc?.name == "Sky Garden.gba.rtc")
        #expect(FileManager.default.fileExists(atPath: folder.appending(path: "\(versions[0].id)/Sky Garden.gba.sav").path))

        // Nothing changed: nothing downloaded.
        let before = source.downloads.count
        let second = try await makeSync(source, sandbox, clock: clock).run()
        #expect(second.stored.isEmpty && second.unchanged == 2)
        #expect(source.downloads.count == before)

        // The save of g2 changed, and only the clock of g1: both get a version.
        source.update("g2") { $0 = .entry("g2", name: "Mosslight Maze", save: "moon-2") }
        source.update("g1") { $0 = .entry("g1", name: "Sky Garden", save: "sky-1", rtc: "clock-2") }
        let third = try await makeSync(source, sandbox, clock: clock).run()
        #expect(Set(third.stored) == ["g1", "g2"])
        #expect(Array(source.downloads.suffix(2)).sorted() == ["g1", "g2"])
        let moon = try await sandbox.vault().versions(deviceId: "dev-1", gameId: "g2")
        #expect(moon.count == 2)
        #expect(moon[0].info.save.sha256 == sha256Hex(Data("moon-2".utf8)))
    }

    /// A sync never deletes a version for the limit, except when it
    /// publishes a new version of that game.
    @Test func aRotationLeftUnfinishedIsFinishedByTheNextVersionOnly() async throws {
        let sandbox = try VaultSandbox()
        let eleven = sandbox.vault(maxVersions: 11)
        let game = VaultGameInfo(gameId: "g1", systemId: "sys-gba", systemName: "Game Boy Advance (GBA)", name: "Sky Garden")
        for i in 1...11 {
            _ = try await eleven.addVersion(console: brick, game: game, save: try sandbox.input("Sky Garden.gba.sav", i == 11 ? "sky" : "old-\(i)"), rtc: nil, modifiedAt: nil)
        }
        let source = StubSaveSource([.entry("g1", name: "Sky Garden", save: "sky")])
        let report = try await makeSync(source, sandbox).run()
        #expect(report.unchanged == 1)
        #expect(try await sandbox.vault().versions(deviceId: "dev-1", gameId: "g1").count == 11)

        source.update("g1") { $0 = .entry("g1", name: "Sky Garden", save: "sky-2") }
        let next = try await makeSync(source, sandbox, clock: TestClock(start: Date(timeIntervalSince1970: 1_795_700_000))).run()
        #expect(next.stored == ["g1"])
        let left = try await sandbox.vault().versions(deviceId: "dev-1", gameId: "g1")
        #expect(left.map(\.info.sequence) == Array((3...12).reversed()))
    }

    @Test func aChecksumMismatchIsRejectedAndTheOtherGamesGoOn() async throws {
        let sandbox = try VaultSandbox()
        var bad = StubSaveSource.Entry.entry("g1", name: "Sky Garden", save: "sky-1")
        bad.served = Data("corrupted".utf8)
        var missing = StubSaveSource.Entry.entry("g3", name: "Gone", save: "x")
        missing.failure = APIError.server(ServerError(status: 404, code: .notFound, message: "No save"))
        let source = StubSaveSource([bad, .entry("g2", name: "Mosslight Maze", save: "moon-1"), missing])
        let report = try await makeSync(source, sandbox).run()
        #expect(report.stored == ["g2"])
        #expect(report.failures.map(\.gameId) == ["g1", "g3"])
        #expect(report.failures[0].error == SyncErrorBox(SaveSyncError.checksumMismatch(file: "Sky Garden.gba.sav")))
        #expect(try await sandbox.vault().versions(deviceId: "dev-1", gameId: "g1").isEmpty)
        // Nothing left in the scratch folder.
        let scratch = sandbox.base.appending(path: "Scratch")
        #expect(((try? FileManager.default.contentsOfDirectory(atPath: scratch.path)) ?? []).isEmpty)
    }

    @Test func losingTheConsoleStopsTheSyncAndKeepsWhatWasStored() async throws {
        let sandbox = try VaultSandbox()
        var offline = StubSaveSource.Entry.entry("g2", name: "Mosslight Maze", save: "moon-1")
        offline.failure = .network(.networkConnectionLost)
        let source = StubSaveSource([.entry("g1", name: "Sky Garden", save: "sky-1"), offline, .entry("g3", name: "Third", save: "3")])
        await #expect(throws: APIError.network(.networkConnectionLost)) {
            try await makeSync(source, sandbox).run()
        }
        #expect(try await sandbox.vault().versions(deviceId: "dev-1", gameId: "g1").count == 1)
        #expect(!source.downloads.contains("g3"))
    }

    @Test func aCancelledSyncStopsBeforeTheNextGame() async throws {
        let sandbox = try VaultSandbox()
        let source = StubSaveSource((1...50).map { .entry("g\($0)", name: "Game \($0)", save: "s\($0)") })
        let sync = makeSync(source, sandbox)
        let task = Task { try await sync.run() }
        task.cancel()
        await #expect(throws: CancellationError.self) { try await task.value }
        #expect(source.downloads.count < 50)
    }

    @Test func withoutTheSystemListTheTagOfTheSavesFolderNamesTheSystem() async throws {
        let sandbox = try VaultSandbox()
        let source = StubSaveSource([.entry("g1", name: "Sky Garden", save: "sky-1")])
        source.systemsFail = true
        _ = try await makeSync(source, sandbox).run()
        #expect(FileManager.default.fileExists(atPath: sandbox.root.appending(path: "My Brick/GBA/Sky Garden").path))
    }

    @Test func systemsWithTheSameLabelGetDistinctFolders() {
        let gba = GameSystem(id: "a", label: "Game Boy Advance", folder: "Game Boy Advance (GBA)", tag: "GBA", gameCount: 1)
        let mgba = GameSystem(id: "b", label: "Game Boy Advance", folder: "Game Boy Advance (MGBA)", tag: "MGBA", gameCount: 1)
        #expect(SaveSync.systemName(gba) != SaveSync.systemName(mgba))
        #expect(SaveSync.systemName(mgba) == "Game Boy Advance (MGBA)")
    }

    @Test func syncsAgainstTheFakePak() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let sandbox = try VaultSandbox()
        let console = VaultConsoleInfo(deviceId: pak.deviceId, name: pak.deviceName)
        func sync() -> SaveSync {
            SaveSync(console: console, source: client, vault: sandbox.vault(), scratchDirectory: sandbox.base.appending(path: "Scratch"))
        }
        let list = try await client.saves()
        #expect(list.saves.count == 4)
        #expect(!list.truncated)
        let sky = try #require(list.saves.first { $0.gameId == skyGarden })
        #expect(sky.rtc?.sizeBytes == 16)
        #expect(sky.path == "Saves/HA/Thistledown Isles.hab.sav")

        let first = try await sync().run()
        #expect(first.stored.count == 4 && first.failures.isEmpty)
        let versions = try await sandbox.vault().versions(deviceId: pak.deviceId, gameId: skyGarden)
        #expect(versions.first?.info.save.name == "Thistledown Isles.hab.sav")
        #expect(versions.first?.info.rtc?.name == "Thistledown Isles.hab.rtc")
        #expect(FileManager.default.fileExists(atPath: sandbox.root.appending(path: "Demo Brick/Handheld Advance (HA)/Thistledown Isles").path))

        pak.play(gameId: skyGarden, content: Data("played".utf8))
        let second = try await sync().run()
        #expect(second.stored == [skyGarden] && second.unchanged == 3)

        // Restoring the first version sends its bytes back.
        let exported = try await sandbox.vault().export(
            deviceId: pak.deviceId, gameId: skyGarden, version: try #require(versions.first),
            to: sandbox.base.appending(path: "Out")
        )
        _ = try await client.replaceSave(gameId: skyGarden, file: exported.save, sha256: try await FileDigest.sha256(of: exported.save))
        let restored = try #require(try await client.saves().saves.first { $0.gameId == skyGarden })
        #expect(restored.sha256 == versions.first?.info.save.sha256)
    }
}

extension SaveSyncTests {
    /// Two games of a system with the same ROM file name in different
    /// folders share one save on NextUI: each keeps its own history, and
    /// restoring one replaces the shared file (after the pak's copy).
    @Test func twoGamesSharingOneSaveKeepTheirOwnHistories() async throws {
        let base = Date(timeIntervalSince1970: 1_790_000_000)
        let library = DemoLibrary(systems: [
            DemoLibrary.System(folder: "Game Boy Advance (GBA)", label: "Game Boy Advance", tag: "GBA", games: [
                DemoLibrary.Game(path: "Europe/Twin Quest.gba", sizeBytes: 1024, modifiedAt: base, hasSave: true),
                DemoLibrary.Game(path: "USA/Twin Quest.gba", sizeBytes: 1024, modifiedAt: base, hasSave: true),
            ]),
        ])
        let pak = FakePak(host: "twin-\(UUID().uuidString.lowercased()).test", library: library)
        pak.register()
        let client = try await pairedClient(pak)
        let europe = DemoLibrary.id("Game Boy Advance (GBA)/Europe/Twin Quest.gba")
        let usa = DemoLibrary.id("Game Boy Advance (GBA)/USA/Twin Quest.gba")
        let sandbox = try VaultSandbox()
        let console = VaultConsoleInfo(deviceId: pak.deviceId, name: "Twin Brick")
        func sync() async throws -> SaveSyncReport {
            try await SaveSync(console: console, source: client, vault: sandbox.vault(), scratchDirectory: sandbox.base.appending(path: "Scratch")).run()
        }

        let listed = try await client.saves().saves
        #expect(Set(listed.map(\.gameId)) == [europe, usa])
        #expect(Set(listed.map(\.path)).count == 1 && Set(listed.map(\.sha256)).count == 1)
        #expect(try await sync().stored.count == 2)

        // Played under either name: both games have a new version.
        pak.play(gameId: europe, content: Data("level 2".utf8))
        #expect(Set(try await sync().stored) == [europe, usa])
        let vault = sandbox.vault()
        let games = try await vault.games(deviceId: pak.deviceId)
        #expect(games.map(\.folderPath) == ["Game Boy Advance (GBA)/Twin Quest", "Game Boy Advance (GBA)/Twin Quest (2)"])
        let usaVersions = try await vault.versions(deviceId: pak.deviceId, gameId: usa)
        let europeVersions = try await vault.versions(deviceId: pak.deviceId, gameId: europe)
        #expect(usaVersions.count == 2 && europeVersions.count == 2)
        #expect(usaVersions.allSatisfy { $0.info.gameId == usa } && europeVersions.allSatisfy { $0.info.gameId == europe })

        // Restoring the first USA version replaces the shared file.
        let first = try #require(usaVersions.last)
        let exported = try await vault.exportVerified(deviceId: pak.deviceId, gameId: usa, version: first, to: sandbox.base.appending(path: "Out"))
        let result = try await client.replaceSave(gameId: usa, file: exported.save, sha256: first.info.save.sha256)
        #expect(result.backup?.reason == .beforeReplace)
        let after = try await client.saves().saves
        #expect(after.allSatisfy { $0.sha256 == first.info.save.sha256 })
        #expect(try await client.saveBackups(gameId: europe).contains { $0.reason == .beforeReplace })
        // The next sync gives each game a third version, in its own folder.
        #expect(Set(try await sync().stored) == [europe, usa])
        #expect(try await sandbox.vault().versions(deviceId: pak.deviceId, gameId: europe).count == 3)
    }
}

/// The fake pak shares what NextUI shares between two games with the same
/// ROM file name (save, states, cheats): its demo controls write where
/// the routes read, for either game.
@Suite("Fake pak: games sharing their files")
struct FakePakSharedFilesTests {
    @Test func demoControlsOnTheSecondGameShowOnBoth() async throws {
        let base = Date(timeIntervalSince1970: 1_790_000_000)
        let library = DemoLibrary(systems: [
            DemoLibrary.System(folder: "Game Boy Advance (GBA)", label: "Game Boy Advance", tag: "GBA", games: [
                DemoLibrary.Game(path: "Europe/Twin Quest.gba", sizeBytes: 1024, modifiedAt: base, hasSave: true, stateSlots: [0], cheatCount: 1),
                DemoLibrary.Game(path: "USA/Twin Quest.gba", sizeBytes: 1024, modifiedAt: base, hasSave: true, stateSlots: [0], cheatCount: 1),
            ]),
        ])
        let pak = FakePak(host: "twin-\(UUID().uuidString.lowercased()).test", library: library)
        pak.register()
        let client = try await pairedClient(pak)
        let europe = DemoLibrary.id("Game Boy Advance (GBA)/Europe/Twin Quest.gba")
        let usa = DemoLibrary.id("Game Boy Advance (GBA)/USA/Twin Quest.gba")

        let before = try await client.states(gameId: usa).slots.first { $0.slot == 0 }?.modifiedAt
        pak.overwriteStateInGame(gameId: usa, slot: 0)
        let usaSlot = try await client.states(gameId: usa).slots.first { $0.slot == 0 }
        let europeSlot = try await client.states(gameId: europe).slots.first { $0.slot == 0 }
        #expect(usaSlot?.modifiedAt != before)
        #expect(europeSlot?.modifiedAt == usaSlot?.modifiedAt)

        let etag = try await client.cheats(gameId: usa).etag
        pak.changeCheatsOnConsole(gameId: usa)
        let usaCheats = try await client.cheats(gameId: usa)
        #expect(usaCheats.etag != etag)
        #expect(usaCheats.cheats.count == 2)
        #expect(try await client.cheats(gameId: europe) == usaCheats)
        #expect(pak.cheatsETag(gameId: europe) == pak.cheatsETag(gameId: usa))
    }
}

extension FakePakSharedFilesTests {
    /// Deleting one of two games sharing their files: what the deletion
    /// took (save, states, cheats; only cheats with keepSave) is gone for
    /// the other, as on the pak.
    @Test(arguments: [false, true])
    func deletingOneGameLeavesTheOtherWithWhatWasKept(keepSave: Bool) async throws {
        let base = Date(timeIntervalSince1970: 1_790_000_000)
        let library = DemoLibrary(systems: [
            DemoLibrary.System(folder: "Game Boy Advance (GBA)", label: "Game Boy Advance", tag: "GBA", games: [
                DemoLibrary.Game(path: "Europe/Twin Quest.gba", sizeBytes: 1024, modifiedAt: base, hasSave: true, stateSlots: [0], cheatCount: 1),
                DemoLibrary.Game(path: "USA/Twin Quest.gba", sizeBytes: 1024, modifiedAt: base, hasSave: true, stateSlots: [0], cheatCount: 1),
            ]),
        ])
        let pak = FakePak(host: "twin-\(UUID().uuidString.lowercased()).test", library: library)
        pak.register()
        let client = try await pairedClient(pak)
        let europe = DemoLibrary.id("Game Boy Advance (GBA)/Europe/Twin Quest.gba")
        let usa = DemoLibrary.id("Game Boy Advance (GBA)/USA/Twin Quest.gba")
        pak.play(gameId: usa, content: Data("shared progress".utf8))

        _ = try await client.deleteGame(id: europe, keepSave: keepSave)
        let saves = try await client.saves().saves
        let states = try await client.states(gameId: usa).slots
        let cheats = try await client.cheats(gameId: usa).cheats
        #expect(cheats.isEmpty)
        if keepSave {
            #expect(saves.map(\.gameId) == [usa])
            #expect(saves.first?.sha256 == sha256Hex(Data("shared progress".utf8)))
            #expect(!states.isEmpty)
        } else {
            #expect(saves.isEmpty)
            #expect(states.isEmpty)
            await #expect(throws: APIError.self) { try await client.downloadSave(gameId: usa, into: downloadsDirectory()) }
        }
    }
}

@Suite("Save listing timeout (M9)")
struct SaveListingTimeoutTests {
    /// The pak answers `GET /saves` only once every save is hashed: the
    /// route waits longer than the session's idle timeout (15 s in the app,
    /// 1 s here), the other routes do not.
    @Test func theSaveListingOutlastsTheSessionTimeout() async throws {
        let body = Data(#"{"saves":[],"truncated":false,"generatedAt":"2026-11-19T10:00:00Z"}"#.utf8)
        let server = try LocalHTTPServer { request in
            Thread.sleep(forTimeInterval: 2)
            return .init(status: 200, headers: ["Content-Type": "application/json"],
                         body: request.path == "/api/v1/saves" ? body : Data(#"{"systems":[]}"#.utf8))
        }
        let port = try await server.start()
        defer { server.stop() }
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 1
        let client = APIClient(
            scheme: "http", // A plain local test server: no TLS.
            endpoint: Endpoint(host: "127.0.0.1", port: port),
            credentials: DeviceCredentials.random(deviceId: FakePak.demoDeviceId),
            session: URLSession(configuration: configuration)
        )
        #expect(APIClient.savesTimeout >= 120)
        let list = try await client.saves()
        #expect(list.saves.isEmpty)
        await #expect(throws: APIError.network(.timedOut)) { try await client.systems() }
    }
}
