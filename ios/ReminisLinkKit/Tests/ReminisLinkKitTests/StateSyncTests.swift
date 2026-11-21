import Foundation
import Testing
@testable import ReminisLinkKit

/// A console with saves and states answered from memory (M19).
final class StubStateSource: SaveSyncSource, StateSyncSource, @unchecked Sendable {
    struct State {
        var summary: StateSummary
        var data: Data
        var thumbnail: Data?
        /// Served instead of `data` (a transfer that went wrong).
        var served: Data?
    }

    private let lock = NSLock()
    private var states: [State]
    let saveSource: StubSaveSource
    /// `GET /states` answers this error (an older pak: 404).
    var listingError: APIError?

    init(saves: [StubSaveSource.Entry] = [], states: [State]) {
        saveSource = StubSaveSource(saves)
        self.states = states
    }

    static func state(_ gameId: String, name: String, slot: Int, _ content: String, thumbnail: String? = "png") -> State {
        let data = Data(content.utf8)
        let thumb = thumbnail.map { Data(("\u{89}PNG" + $0).utf8) }
        return State(
            summary: StateSummary(
                gameId: gameId, systemId: "sys-gba", gameName: name, fileName: "\(name).gba", slot: slot,
                path: ".userdata/shared/GBA-gpsp/\(name).gba.st\(slot)", sizeBytes: Int64(data.count),
                modifiedAt: Date(timeIntervalSince1970: 1_790_000_000), sha256: sha256Hex(data),
                thumbnail: thumb.map { StateThumbnailFile(sizeBytes: Int64($0.count), sha256: sha256Hex($0)) }
            ),
            data: data, thumbnail: thumb
        )
    }

    func update(_ gameId: String, slot: Int, _ change: (inout State) -> Void) {
        lock.withLock {
            guard let i = states.firstIndex(where: { $0.summary.gameId == gameId && $0.summary.slot == slot }) else { return }
            change(&states[i])
        }
    }

    func set(_ gameId: String, slot: Int, _ content: String) {
        let fresh = Self.state(gameId, name: "x", slot: slot, content)
        update(gameId, slot: slot) { state in
            state.data = fresh.data
            state.summary = StateSummary(
                gameId: state.summary.gameId, systemId: state.summary.systemId, gameName: state.summary.gameName,
                fileName: state.summary.fileName, slot: slot, path: state.summary.path, sizeBytes: fresh.summary.sizeBytes,
                modifiedAt: state.summary.modifiedAt, sha256: fresh.summary.sha256, thumbnail: state.summary.thumbnail
            )
        }
    }

    func saves() async throws -> SaveList { try await saveSource.saves() }
    func systems() async throws -> [GameSystem] { try await saveSource.systems() }
    func downloadSave(gameId: String, into directory: URL) async throws -> DownloadedFile {
        try await saveSource.downloadSave(gameId: gameId, into: directory)
    }
    func downloadSaveClock(gameId: String, into directory: URL) async throws -> DownloadedFile {
        try await saveSource.downloadSaveClock(gameId: gameId, into: directory)
    }

    func allStates() async throws -> StateList {
        if let listingError { throw listingError }
        return lock.withLock { StateList(states: states.map(\.summary), truncated: false, generatedAt: Date()) }
    }

    func downloadState(gameId: String, slot: Int, into directory: URL) async throws -> DownloadedFile {
        let state = try find(gameId, slot)
        return try write(state.served ?? state.data, name: "\(state.summary.fileName).st\(slot)", into: directory)
    }

    func downloadStateThumbnail(gameId: String, slot: Int, into directory: URL) async throws -> DownloadedFile {
        let state = try find(gameId, slot)
        guard let thumbnail = state.thumbnail else { throw APIError.server(ServerError(status: 404, code: .notFound, message: "No thumbnail")) }
        return try write(thumbnail, name: "\(state.summary.fileName).\(slot).bmp", into: directory)
    }

    private func find(_ gameId: String, _ slot: Int) throws -> State {
        guard let state = lock.withLock({ states.first { $0.summary.gameId == gameId && $0.summary.slot == slot } }) else {
            throw APIError.server(ServerError(status: 404, code: .notFound, message: "State not found"))
        }
        return state
    }

    private func write(_ data: Data, name: String, into directory: URL) throws -> DownloadedFile {
        let folder = directory.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let url = folder.appending(path: name)
        try data.write(to: url)
        return DownloadedFile(url: url)
    }
}

@Suite("State sync to the vault (M19)")
struct StateSyncTests {
    func makeSync(_ source: any SaveSyncSource, _ sandbox: borrowing VaultSandbox, clock: TestClock = TestClock()) -> SaveSync {
        SaveSync(console: brick, source: source, vault: sandbox.vault(clock: clock), scratchDirectory: sandbox.base.appending(path: "Scratch"))
    }

    @Test func statesAreVersionedPerSlotNextToTheSaves() async throws {
        let sandbox = try VaultSandbox()
        let source = StubStateSource(
            saves: [.entry("g1", name: "Sky", save: "save-1")],
            states: [
                StubStateSource.state("g1", name: "Sky", slot: 0, "state-0"),
                StubStateSource.state("g1", name: "Sky", slot: 3, "state-3", thumbnail: nil),
            ]
        )
        let clock = TestClock()
        let log = ProgressLog()
        let first = try await makeSync(source, sandbox, clock: clock).run { log.add($0) }
        #expect(first.stored == ["g1"])
        #expect(first.states?.stored == ["g1/0", "g1/3"])
        #expect(first.states?.total == 2 && first.states?.failures.isEmpty == true)
        #expect(log.reports.last?.statesChecked == 2 && log.reports.last?.fraction == 1)

        let vault = sandbox.vault(clock: clock)
        let states = try await vault.stateVersions(deviceId: brick.deviceId, gameId: "g1")
        #expect(states.map(\.info.slot) == [0, 3])
        #expect(states[0].info.thumbnail?.name == "Sky.gba.st0.png" && states[1].info.thumbnail == nil)
        #expect(states[0].id.hasSuffix(" state 0"))
        // The save versions do not see the states, and the other way round.
        #expect(try await vault.versions(deviceId: brick.deviceId, gameId: "g1").count == 1)
        let games = try await vault.games(deviceId: brick.deviceId)
        #expect(games.first?.stateSlots == [0, 3] && games.first?.versionCount == 1)

        // Nothing changed: nothing new.
        let second = try await makeSync(source, sandbox, clock: clock).run()
        #expect(second.states?.stored.isEmpty == true && second.states?.unchanged == 2)

        // One slot changed: a new version of that slot only.
        source.set("g1", slot: 3, "state-3b")
        let third = try await makeSync(source, sandbox, clock: clock).run()
        #expect(third.states?.stored == ["g1/3"])
        let after = try await vault.stateVersions(deviceId: brick.deviceId, gameId: "g1")
        #expect(after.filter { $0.info.slot == 3 }.map(\.info.sequence) == [2, 1])
        #expect(after.filter { $0.info.slot == 0 }.count == 1)

        // A version is exported verified.
        let out = sandbox.base.appending(path: "Out")
        let exported = try await vault.exportStateVerified(deviceId: brick.deviceId, gameId: "g1", version: after[0], to: out)
        #expect(try Data(contentsOf: exported.state) == Data("state-0".utf8))
        #expect(exported.thumbnail != nil)
    }

    @Test func aDamagedDownloadStoresNothingAndTheNextSyncRetries() async throws {
        let sandbox = try VaultSandbox()
        let source = StubStateSource(states: [StubStateSource.state("g1", name: "Sky", slot: 1, "good")])
        source.update("g1", slot: 1) { $0.served = Data("bad".utf8) }
        let clock = TestClock()
        let report = try await makeSync(source, sandbox, clock: clock).run()
        #expect(report.states?.failures.map(\.id) == ["g1/1"])
        #expect(try await sandbox.vault(clock: clock).stateVersions(deviceId: brick.deviceId, gameId: "g1").isEmpty)

        source.update("g1", slot: 1) { $0.served = nil }
        let next = try await makeSync(source, sandbox, clock: clock).run()
        #expect(next.states?.stored == ["g1/1"])
    }

    @Test func anOlderPakSyncsTheSavesOnly() async throws {
        let sandbox = try VaultSandbox()
        let source = StubStateSource(saves: [.entry("g1", name: "Sky", save: "s")], states: [])
        source.listingError = .server(ServerError(status: 404, code: .notFound, message: "Not found"))
        let report = try await makeSync(source, sandbox).run()
        #expect(report.stored == ["g1"] && report.states == nil)
        // A source without states at all.
        let plain = try await makeSync(StubSaveSource([.entry("g2", name: "Moon", save: "m")]), sandbox).run()
        #expect(plain.states == nil && plain.stored == ["g2"])
    }

    @Test func eachSlotKeepsItsOwnLimit() async throws {
        let sandbox = try VaultSandbox()
        let clock = TestClock()
        let vault = sandbox.vault(maxVersions: 3, clock: clock)
        let game = VaultGameInfo(gameId: "g1", systemId: "sys-gba", systemName: "Game Boy Advance (GBA)", name: "Sky")
        _ = try await vault.addVersion(console: brick, game: game, save: try sandbox.input("Sky.gba.sav", "save"), rtc: nil, modifiedAt: nil)
        _ = try await vault.addStateVersion(console: brick, game: game, slot: 1, state: try sandbox.input("Sky.gba.st1", "one"), thumbnail: nil, modifiedAt: nil)
        for n in 0..<5 {
            _ = try await vault.addStateVersion(
                console: brick, game: game, slot: 0, state: try sandbox.input("Sky.gba.st0", "zero-\(n)"),
                thumbnail: try sandbox.input("Sky.gba.st0.png", "thumb-\(n)"), modifiedAt: nil
            )
        }
        let states = try await vault.stateVersions(deviceId: brick.deviceId, gameId: "g1")
        #expect(states.filter { $0.info.slot == 0 }.map(\.info.sequence) == [5, 4, 3])
        #expect(states.filter { $0.info.slot == 1 }.count == 1)
        #expect(try await vault.versions(deviceId: brick.deviceId, gameId: "g1").count == 1)

        // A version changed by hand is never deleted.
        let oldest = try #require(states.first { $0.info.slot == 0 && $0.info.sequence == 3 })
        let folder = sandbox.root.appending(path: "My Brick/Game Boy Advance (GBA)/Sky/\(oldest.id)")
        try Data("by hand".utf8).write(to: folder.appending(path: "notes.txt"))
        for n in 5..<8 {
            _ = try await vault.addStateVersion(
                console: brick, game: game, slot: 0, state: try sandbox.input("Sky.gba.st0", "zero-\(n)"), thumbnail: nil, modifiedAt: nil
            )
        }
        let kept = try await vault.stateVersions(deviceId: brick.deviceId, gameId: "g1").filter { $0.info.slot == 0 }.map(\.info.sequence)
        #expect(kept == [8, 7, 6, 3])
    }

    /// Two devices synced the same console before iCloud delivered the
    /// other's folders: the state versions of both game folders are read
    /// as one series, and the folders converge by moves only.
    @Test func stateVersionsOfDuplicateGameFoldersConverge() async throws {
        let sandbox = try VaultSandbox()
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let game = VaultGameInfo(gameId: "g1", systemId: "sys-gba", systemName: "Game Boy Advance (GBA)", name: "Sky")
        let clock = TestClock()
        let phone = sandbox.vault(clock: clock)
        let other = FileSaveVault(location: .iCloud, root: mac, stagingDirectory: sandbox.staging, timeZone: TimeZone(identifier: "Europe/Paris")!, now: { clock.next() })
        _ = try await phone.addStateVersion(console: brick, game: game, slot: 0, state: try sandbox.input("Sky.gba.st0", "a"), thumbnail: nil, modifiedAt: nil)
        _ = try await other.addStateVersion(console: brick, game: game, slot: 0, state: try sandbox.input("Sky.gba.st0", "b"), thumbnail: nil, modifiedAt: nil)
        // iCloud delivers the Mac's game folder next to the iPhone's: "Sky 2".
        let system = sandbox.root.appending(path: "My Brick/Game Boy Advance (GBA)")
        try FileManager.default.moveItem(at: mac.appending(path: "My Brick/Game Boy Advance (GBA)/Sky"), to: system.appending(path: "Sky 2"))

        let reader = sandbox.vault(clock: clock)
        let both = try await reader.stateVersions(deviceId: brick.deviceId, gameId: "g1")
        #expect(both.count == 2)
        // The next publication numbers after both.
        let next = try await reader.addStateVersion(console: brick, game: game, slot: 0, state: try sandbox.input("Sky.gba.st0", "c"), thumbnail: nil, modifiedAt: nil)
        #expect(next.info.sequence == 2)
        // Tidied by moves: one game folder left, every version kept.
        _ = try await sandbox.vault(clock: clock).games(deviceId: brick.deviceId)
        let folders = try FileManager.default.contentsOfDirectory(atPath: system.path).filter { !$0.hasPrefix(".") }
        #expect(folders == ["Sky"])
        #expect(try await sandbox.vault(clock: clock).stateVersions(deviceId: brick.deviceId, gameId: "g1").count == 3)
    }

    @Test func stateManifestsDecode() throws {
        let json = """
        {"gameId":"g","slot":2,"sequence":1,"syncedAt":"2026-11-21T10:00:00Z","modifiedAt":null,
         "state":{"name":"a.st2","sha256":"\(String(repeating: "a", count: 64))","sizeBytes":3},"thumbnail":null}
        """
        let info = try ProtocolJSON.decoder().decode(StateVersionInfo.self, from: Data(json.utf8))
        #expect(info.series == "state/2" && info.files.count == 1)
        // A state manifest is never read as a save version.
        #expect((try? ProtocolJSON.decoder().decode(SaveVersionInfo.self, from: Data(json.utf8))) == nil)
        #expect(FileSaveVault.stateVersionName(Date(timeIntervalSince1970: 0), slot: 4, timeZone: TimeZone(identifier: "UTC")!) == "1970-01-01 00.00.00 state 4")
    }
}
