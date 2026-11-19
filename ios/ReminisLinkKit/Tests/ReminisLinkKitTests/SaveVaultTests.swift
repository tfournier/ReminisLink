import CryptoKit
import Foundation
import Testing
@testable import ReminisLinkKit

/// A temporary folder holding a vault root and its staging folder.
struct VaultSandbox: ~Copyable {
    let base: URL
    var root: URL { base.appending(path: "Vault", directoryHint: .isDirectory) }
    var staging: URL { base.appending(path: "Staging", directoryHint: .isDirectory) }
    var inputs: URL { base.appending(path: "Inputs", directoryHint: .isDirectory) }

    init() throws {
        base = FileManager.default.temporaryDirectory.appending(path: "rl-vault-\(UUID().uuidString)", directoryHint: .isDirectory)
        try FileManager.default.createDirectory(at: base.appending(path: "Inputs"), withIntermediateDirectories: true)
    }

    deinit { try? FileManager.default.removeItem(at: base) }

    func vault(maxVersions: Int = SaveVaultLimits.maxVersions, clock: TestClock = TestClock()) -> FileSaveVault {
        FileSaveVault(
            location: .local, root: root, stagingDirectory: staging, maxVersions: maxVersions,
            timeZone: TimeZone(identifier: "Europe/Paris")!, now: { clock.next() }
        )
    }

    /// A file to add, with its SHA-256.
    func input(_ name: String, _ content: String) throws -> NewVersionFile {
        let url = inputs.appending(path: "\(UUID().uuidString)-\(name)")
        let data = Data(content.utf8)
        try data.write(to: url)
        return NewVersionFile(url: url, name: name, sha256: sha256Hex(data))
    }
}

/// A clock that moves one second at each reading.
final class TestClock: @unchecked Sendable {
    private let lock = NSLock()
    private var current: Date

    init(start: Date = Date(timeIntervalSince1970: 1_795_609_330)) { current = start }

    func next() -> Date {
        lock.withLock {
            defer { current += 1 }
            return current
        }
    }
}

let brick = VaultConsoleInfo(deviceId: "dev-1", name: "My Brick")
let courier = VaultGameInfo(gameId: "g-courier", systemId: "s-gba", systemName: "Game Boy Advance", name: "Starlight Courier")

@Suite("Save vault (M9)")
struct SaveVaultTests {
    @Test func aVersionIsStoredInConsoleSystemGameDateFolders() async throws {
        let sandbox = try VaultSandbox()
        let vault = sandbox.vault()
        let save = try sandbox.input("Starlight Courier.gba.sav", "save-1")
        let rtc = try sandbox.input("Starlight Courier.gba.rtc", "clock")
        let modified = Date(timeIntervalSince1970: 1_795_600_000)
        let version = try await vault.addVersion(console: brick, game: courier, save: save, rtc: rtc, modifiedAt: modified)
        // 1_795_609_330 = 2026-11-25 13:22:10 in Paris.
        #expect(version.id == "2026-11-25 13.22.10")
        let folder = sandbox.root.appending(path: "My Brick/Game Boy Advance/Starlight Courier/\(version.id)")
        #expect(try String(contentsOf: folder.appending(path: "Starlight Courier.gba.sav"), encoding: .utf8) == "save-1")
        #expect(try String(contentsOf: folder.appending(path: "Starlight Courier.gba.rtc"), encoding: .utf8) == "clock")
        #expect(version.info.save.sha256 == sha256Hex(Data("save-1".utf8)))
        #expect(version.info.save.sizeBytes == 6)
        #expect(version.info.rtc?.name == "Starlight Courier.gba.rtc")
        #expect(version.info.modifiedAt == modified)
        // The inputs were moved in; the staging folder is left empty.
        #expect(!FileManager.default.fileExists(atPath: save.url.path))
        #expect(try FileManager.default.contentsOfDirectory(atPath: sandbox.staging.path).isEmpty)

        let latest = (try await vault.latestVersion(deviceId: "dev-1", gameId: "g-courier"))
        #expect(latest == version)
    }

    @Test func rotationKeepsTheTenNewestVersions() async throws {
        let sandbox = try VaultSandbox()
        let vault = sandbox.vault()
        var added: [SaveVersion] = []
        for i in 1...12 {
            let save = try sandbox.input("Game.sav", "save-\(i)")
            added.append(try await vault.addVersion(console: brick, game: courier, save: save, rtc: nil, modifiedAt: nil))
        }
        let versions = (try await vault.versions(deviceId: "dev-1", gameId: "g-courier"))
        #expect(versions.count == 10)
        #expect(versions.map(\.id) == added.reversed().prefix(10).map(\.id))
        let game = sandbox.root.appending(path: "My Brick/Game Boy Advance/Starlight Courier")
        let folders = try FileManager.default.contentsOfDirectory(atPath: game.path).filter { !$0.hasPrefix(".") }
        #expect(folders.count == 10)
        #expect(!folders.contains(added[0].id))
        #expect(!folders.contains(added[1].id))
    }

    @Test func aClockMovedBackNeverDeletesTheVersionJustPublished() async throws {
        let sandbox = try VaultSandbox()
        let vault = sandbox.vault()
        for i in 1...10 {
            _ = try await vault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "save-\(i)"), rtc: nil, modifiedAt: nil)
        }
        // The iPhone clock is set a year back.
        let past = sandbox.vault(clock: TestClock(start: Date(timeIntervalSince1970: 1_700_000_000)))
        let added = try await past.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "save-11"), rtc: nil, modifiedAt: nil)
        let versions = try await past.versions(deviceId: "dev-1", gameId: "g-courier")
        #expect(versions.count == 10)
        #expect(versions.first?.id == added.id)
        #expect(versions.first?.info.sequence == 11)
        #expect(versions.first?.info.save.sha256 == sha256Hex(Data("save-11".utf8)))
        // The oldest published (sequence 1) went, whatever its date.
        #expect(!versions.contains { $0.info.sequence == 1 })
    }

    @Test func anUnfinishedRotationIsFinishedLater() async throws {
        let sandbox = try VaultSandbox()
        // Eleven versions, as left by a rotation interrupted after the move.
        let eleven = sandbox.vault(maxVersions: 11)
        for i in 1...11 {
            _ = try await eleven.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "save-\(i)"), rtc: nil, modifiedAt: nil)
        }
        let vault = sandbox.vault()
        #expect(try await vault.versions(deviceId: "dev-1", gameId: "g-courier").count == 11)
        let retained = try await vault.retainedVersions(deviceId: "dev-1", gameId: "g-courier")
        #expect(retained.map(\.info.sequence) == Array((2...11).reversed()))
        #expect(try await vault.versions(deviceId: "dev-1", gameId: "g-courier").count == 10)
    }

    @Test func aVerifiedExportRefusesACopyThatIsNotTheVersionChosen() async throws {
        let sandbox = try VaultSandbox()
        let vault = sandbox.vault()
        let version = try await vault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "chosen"), rtc: nil, modifiedAt: nil)
        let out = sandbox.base.appending(path: "Out")
        let good = try await vault.exportVerified(deviceId: "dev-1", gameId: "g-courier", version: version, to: out)
        #expect(try Data(contentsOf: good.save) == Data("chosen".utf8))
        // Another vault holds a version with the same folder name but other bytes.
        let other = FileSaveVault(location: .iCloud, root: sandbox.base.appending(path: "Other"), stagingDirectory: sandbox.staging,
                                  timeZone: TimeZone(identifier: "Europe/Paris")!, now: { Date(timeIntervalSince1970: 1_795_609_330) })
        let twin = try await other.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "other bytes"), rtc: nil, modifiedAt: nil)
        #expect(twin.id == version.id)
        await #expect(throws: SaveVaultError.versionMismatch) {
            try await other.exportVerified(deviceId: "dev-1", gameId: "g-courier", version: version, to: out)
        }
        // The refused copy is not left behind.
        #expect(try FileManager.default.contentsOfDirectory(atPath: out.path).count == 1)
    }

    @Test func aFolderThatIsNotAVersionIsNeverCountedNorDeleted() async throws {
        let sandbox = try VaultSandbox()
        let vault = sandbox.vault(maxVersions: 2)
        for i in 1...2 {
            let save = try sandbox.input("Game.sav", "save-\(i)")
            _ = (try await vault.addVersion(console: brick, game: courier, save: save, rtc: nil, modifiedAt: nil))
        }
        let game = sandbox.root.appending(path: "My Brick/Game Boy Advance/Starlight Courier")
        // Put there by the user in Files.
        let mine = game.appending(path: "0000 my copy")
        try FileManager.default.createDirectory(at: mine, withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: mine.appending(path: "Game.sav"))
        let save = try sandbox.input("Game.sav", "save-3")
        _ = (try await vault.addVersion(console: brick, game: courier, save: save, rtc: nil, modifiedAt: nil))
        #expect(FileManager.default.fileExists(atPath: mine.appending(path: "Game.sav").path))
        let versions = (try await vault.versions(deviceId: "dev-1", gameId: "g-courier"))
        #expect(versions.count == 2)
    }

    @Test func foldersAreFoundByIdentifierNotByName() async throws {
        let sandbox = try VaultSandbox()
        let save1 = try sandbox.input("A.sav", "a")
        let first = sandbox.vault()
        _ = (try await first.addVersion(console: brick, game: courier, save: save1, rtc: nil, modifiedAt: nil))

        // A new instance (new sync), the console renamed since, and another
        // game with the same name.
        let vault = sandbox.vault(clock: TestClock(start: Date(timeIntervalSince1970: 1_795_700_000)))
        let renamed = VaultConsoleInfo(deviceId: "dev-1", name: "Renamed")
        let save2 = try sandbox.input("A.sav", "b")
        _ = (try await vault.addVersion(console: renamed, game: courier, save: save2, rtc: nil, modifiedAt: nil))
        let twin = VaultGameInfo(gameId: "g-twin", systemId: "s-gba", systemName: "Game Boy Advance", name: "Starlight Courier")
        let save3 = try sandbox.input("A.sav", "c")
        _ = (try await vault.addVersion(console: renamed, game: twin, save: save3, rtc: nil, modifiedAt: nil))

        let consoles = (try await vault.consoles())
        #expect(consoles.map(\.folderName) == ["My Brick"])
        let games = (try await vault.games(deviceId: "dev-1"))
        #expect(games.map(\.folderPath) == ["Game Boy Advance/Starlight Courier", "Game Boy Advance/Starlight Courier (2)"])
        #expect(games.first { $0.id == "g-courier" }?.versionCount == 2)
        #expect(games.first { $0.id == "g-twin" }?.versionCount == 1)
    }

    @Test func twoVersionsInTheSameSecondGetDistinctFolders() async throws {
        let sandbox = try VaultSandbox()
        let fixed = Date(timeIntervalSince1970: 1_795_609_330)
        let vault = FileSaveVault(location: .local, root: sandbox.root, stagingDirectory: sandbox.staging, now: { fixed })
        var ids: [String] = []
        for i in 1...2 {
            let save = try sandbox.input("Game.sav", "s\(i)")
            ids.append((try await vault.addVersion(console: brick, game: courier, save: save, rtc: nil, modifiedAt: nil)).id)
        }
        #expect(ids[0] != ids[1])
        #expect(ids[1].hasSuffix(" (2)"))
    }

    @Test func exportCopiesTheFilesOutOfTheVault() async throws {
        let sandbox = try VaultSandbox()
        let vault = sandbox.vault()
        let save = try sandbox.input("Game.sav", "content")
        let rtc = try sandbox.input("Game.rtc", "clock")
        let version = (try await vault.addVersion(console: brick, game: courier, save: save, rtc: rtc, modifiedAt: nil))
        let out = sandbox.base.appending(path: "Out")
        let exported = try await vault.export(deviceId: "dev-1", gameId: "g-courier", versionId: version.id, to: out)
        #expect(exported.save.lastPathComponent == "Game.sav")
        #expect(try Data(contentsOf: exported.save) == Data("content".utf8))
        #expect(try exported.rtc.map { try Data(contentsOf: $0) } == Data("clock".utf8))
        // The vault keeps its copy.
        #expect((try await vault.versions(deviceId: "dev-1", gameId: "g-courier")).count == 1)
        await #expect(throws: SaveVaultError.versionNotFound) {
            (try await vault.export(deviceId: "dev-1", gameId: "g-courier", versionId: "nope", to: out))
        }
    }

    @Test func unsafeNamesAreRefused() async throws {
        let sandbox = try VaultSandbox()
        let vault = sandbox.vault()
        for name in ["../escape.sav", ".hidden", "", ".version.json", "a/b.sav"] {
            let save = try sandbox.input("x", "x")
            let bad = NewVersionFile(url: save.url, name: name, sha256: save.sha256)
            await #expect(throws: SaveVaultError.self) {
                (try await vault.addVersion(console: brick, game: courier, save: bad, rtc: nil, modifiedAt: nil))
            }
        }
        #expect(!FileManager.default.fileExists(atPath: sandbox.root.appending(path: "My Brick").path))
    }

    @Test func folderNamesAreSafe() {
        #expect(FileSaveVault.folderName("Pokémon: Red/Blue?") == "Pokémon_ Red_Blue_")
        #expect(FileSaveVault.folderName("..hidden.") == "hidden")
        #expect(FileSaveVault.folderName("   ") == "_")
        #expect(FileSaveVault.folderName(String(repeating: "a", count: 300)).count == 100)
    }

    @Test func withoutICloudTheLocalFolderIsUsed() async throws {
        let sandbox = try VaultSandbox()
        let vault = await SaveVaultLocator.resolve(
            containerIdentifier: "iCloud.link.reminis.app", localRoot: sandbox.root,
            stagingDirectory: sandbox.staging, lookup: { _ in nil }
        )
        #expect(vault.location == .local)
        #expect(vault.root == sandbox.root)
    }

    @Test func withICloudTheDocumentsOfTheContainerAreUsed() async throws {
        let sandbox = try VaultSandbox()
        let container = sandbox.base.appending(path: "Container")
        let vault = await SaveVaultLocator.resolve(
            containerIdentifier: "iCloud.link.reminis.app", localRoot: sandbox.root,
            stagingDirectory: sandbox.staging, lookup: { id in id == "iCloud.link.reminis.app" ? container : nil }
        )
        #expect(vault.location == .iCloud)
        #expect(vault.root.standardizedFileURL == container.appending(path: "Documents").standardizedFileURL)
        #expect(FileManager.default.fileExists(atPath: container.appending(path: "Documents").path))
    }
}
