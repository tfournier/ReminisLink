import Foundation
import Testing
@testable import ReminisLinkKit

/// Two devices of one Apple account writing the same vault in iCloud
/// Drive. Each device writes its own folder tree (a root per device), then
/// "iCloud" delivers a folder of one into the other, renaming it with
/// iCloud's suffix (`X 2`) when the name is taken.
@Suite("Save vault convergence (several devices)")
struct VaultConvergenceTests {
    static let gba = "Game Boy Advance (GBA)"
    static let paris = TimeZone(identifier: "Europe/Paris")!
    static let later = Date(timeIntervalSince1970: 1_795_700_000)

    func vault(_ root: URL, _ sandbox: borrowing VaultSandbox, maxVersions: Int = SaveVaultLimits.maxVersions, clock: TestClock = TestClock()) -> FileSaveVault {
        FileSaveVault(location: .iCloud, root: root, stagingDirectory: sandbox.staging, maxVersions: maxVersions, timeZone: Self.paris, now: { clock.next() })
    }

    func game(_ id: String, _ name: String, system: String = gba) -> VaultGameInfo {
        VaultGameInfo(gameId: id, systemId: "sys-gba", systemName: system, name: name)
    }

    /// Moves `item` into `folder` as iCloud does: `<name> 2` (then 3...) when the name is taken.
    @discardableResult
    func deliver(_ item: URL, into folder: URL) throws -> URL {
        let manager = FileManager.default
        try manager.createDirectory(at: folder, withIntermediateDirectories: true)
        var target = folder.appending(path: item.lastPathComponent)
        var n = 2
        while manager.fileExists(atPath: target.path) {
            target = folder.appending(path: "\(item.lastPathComponent) \(n)")
            n += 1
        }
        try manager.moveItem(at: item, to: target)
        return target
    }

    /// Replaces a file by its iCloud placeholder; returns its content.
    @discardableResult
    func evict(_ file: URL) throws -> Data {
        let data = try Data(contentsOf: file)
        try FileManager.default.removeItem(at: file)
        try Data().write(to: file.deletingLastPathComponent().appending(path: ".\(file.lastPathComponent).icloud"))
        return data
    }

    func download(_ file: URL, _ data: Data) throws {
        try FileManager.default.removeItem(at: file.deletingLastPathComponent().appending(path: ".\(file.lastPathComponent).icloud"))
        try data.write(to: file)
    }

    /// Every item under `root` (hidden ones included), with the content of files.
    func tree(_ root: URL) -> [String: Data] {
        var result: [String: Data] = [:]
        for case let path as String in FileManager.default.enumerator(atPath: root.path) ?? NSEnumerator() {
            let url = root.appending(path: path)
            result[path] = FileSaveVault.isDirectory(url) ? Data() : (try? Data(contentsOf: url)) ?? Data("?".utf8)
        }
        return result
    }

    /// The version folders (files with a `.version.json`) under `root`.
    func versionFolders(_ root: URL) -> Int {
        tree(root).keys.filter { $0.hasSuffix("/.version.json") }.count
    }

    func names(_ folder: URL) throws -> [String] {
        try FileManager.default.contentsOfDirectory(atPath: folder.path).filter { !$0.hasPrefix(".") }.sorted()
    }

    func sha(_ s: String) -> String { sha256Hex(Data(s.utf8)) }

    /// The bug seen on 2026-11-20: the iPhone and a Mac synced the same
    /// console minutes apart; iCloud had delivered the console folder but
    /// not yet the system folder, so both created "Game Boy Advance (GBA)"
    /// and iCloud renamed one "Game Boy Advance (GBA) 2".
    @Test func theGBAAndGBA2FoldersConvergeAndTheNextSyncIsANoOp() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let source = StubSaveSource([
            .entry("g1", name: "Sky Garden", save: "sky-1", rtc: "clock-1"),
            .entry("g2", name: "Moonlit Maze", save: "moon-1"),
            .entry("g3", name: "Starlight Courier", save: "star-1"),
        ])
        let scratch = sandbox.base.appending(path: "Scratch")
        _ = try await SaveSync(console: brick, source: source, vault: vault(iPhone, sandbox), scratchDirectory: scratch).run()
        // iCloud delivered the console folder (and its .console.json) only.
        try FileManager.default.createDirectory(at: mac.appending(path: "My Brick"), withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: iPhone.appending(path: "My Brick/.console.json"), to: mac.appending(path: "My Brick/.console.json"))
        _ = try await SaveSync(console: brick, source: source, vault: vault(mac, sandbox, clock: TestClock(start: Self.later)), scratchDirectory: scratch).run()
        try deliver(mac.appending(path: "My Brick/\(Self.gba)"), into: iPhone.appending(path: "My Brick"))
        #expect(try names(iPhone.appending(path: "My Brick")) == [Self.gba, "\(Self.gba) 2"])

        let games = try await vault(iPhone, sandbox).games(deviceId: "dev-1")
        #expect(games.map(\.id).sorted() == ["g1", "g2", "g3"])
        // The same save synced by both devices is shown once, both copies kept.
        #expect(games.allSatisfy { $0.versionCount == 1 && $0.folderPath.hasPrefix("\(Self.gba)/") })
        #expect(versionFolders(iPhone) == 6)
        // Tidied by moves: one system folder, one folder per game.
        #expect(try names(iPhone.appending(path: "My Brick")) == [Self.gba])
        #expect(try names(iPhone.appending(path: "My Brick/\(Self.gba)")) == ["Moonlit Maze", "Sky Garden", "Starlight Courier"])
        #expect(try names(iPhone.appending(path: "My Brick/\(Self.gba)/Sky Garden")).count == 2)

        // The next sync stores nothing and changes nothing.
        let before = tree(iPhone)
        let report = try await SaveSync(console: brick, source: source, vault: vault(iPhone, sandbox), scratchDirectory: scratch).run()
        #expect(report.stored.isEmpty && report.unchanged == 3)
        #expect(tree(iPhone) == before)
    }

    /// Both devices created the console folder; versions are split
    /// between "My Brick" and "My Brick 2".
    @Test func duplicateConsoleFoldersAreReadTogetherThenTidied() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let macVault = vault(mac, sandbox, clock: TestClock(start: Self.later))
        _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "b"), rtc: nil, modifiedAt: nil)
        _ = try await macVault.addVersion(console: brick, game: game("g2", "Moonlit Maze"), save: try sandbox.input("Maze.sav", "m"), rtc: nil, modifiedAt: nil)
        try deliver(mac.appending(path: "My Brick"), into: iPhone)

        let merged = vault(iPhone, sandbox)
        #expect(try await merged.consoles().map(\.folderName) == ["My Brick"])
        let versions = try await merged.versions(deviceId: "dev-1", gameId: "g1")
        #expect(versions.map(\.info.save.sha256) == [sha("b"), sha("a")])
        #expect(try await merged.versions(deviceId: "dev-1", gameId: "g2").count == 1)
        #expect(try names(iPhone) == ["My Brick"])
        #expect(try names(iPhone.appending(path: "My Brick/\(Self.gba)")) == ["Moonlit Maze", "Starlight Courier"])
        #expect(versionFolders(iPhone) == 4)

        // A second load changes nothing.
        let before = tree(iPhone)
        let again = vault(iPhone, sandbox)
        _ = try await again.consoles()
        _ = try await again.games(deviceId: "dev-1")
        #expect(tree(iPhone) == before)
    }

    /// Game folders "X" and "X 2" with 7 versions each: all 14 are kept
    /// and listed until the next version of the game is published.
    @Test func splitVersionsAreAllKeptUntilTheNextPublication() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let mine = vault(iPhone, sandbox)
        for i in 1...7 {
            _ = try await mine.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "iphone-\(i)"), rtc: nil, modifiedAt: nil)
        }
        let macVault = vault(mac, sandbox, clock: TestClock(start: Self.later))
        for i in 1...7 {
            _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "mac-\(i)"), rtc: nil, modifiedAt: nil)
        }
        let system = iPhone.appending(path: "My Brick/\(Self.gba)")
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: system)

        let merged = vault(iPhone, sandbox)
        let versions = try await merged.versions(deviceId: "dev-1", gameId: "g1")
        #expect(versions.count == 14)
        #expect(versions.map(\.info.sequence) == [7, 7, 6, 6, 5, 5, 4, 4, 3, 3, 2, 2, 1, 1])
        #expect(versions.prefix(2).map(\.info.save.sha256) == [sha("mac-7"), sha("iphone-7")])
        #expect(try names(system) == ["Starlight Courier"])
        #expect(versionFolders(iPhone) == 14)

        // A sync that publishes nothing for this game deletes nothing.
        let source = StubSaveSource([.entry("g9", name: "Other", save: "o")])
        _ = try await SaveSync(console: brick, source: source, vault: vault(iPhone, sandbox), scratchDirectory: sandbox.base.appending(path: "Scratch")).run()
        #expect(try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1").count == 14)

        // The next version is numbered after every one, then the limit applies.
        let next = try await merged.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "new"), rtc: nil, modifiedAt: nil)
        #expect(next.info.sequence == 8)
        let after = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        #expect(after.map(\.info.sequence) == [8, 7, 7, 6, 6, 5, 5, 4, 4, 3])
    }

    /// Identical versions are listed once when published one after the
    /// other, never deleted; a save that went back to an older content
    /// stays a version (A@1, B@8, A@9: all three listed, A@9 newest).
    @Test func identicalVersionsAreShownOnceAndNeverDeleted() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "A"), rtc: nil, modifiedAt: nil)
        let macVault = vault(mac, sandbox, clock: TestClock(start: Self.later))
        for content in ["A", "B", "A"] {
            _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", content), rtc: nil, modifiedAt: nil)
        }
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))

        let versions = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        #expect(versions.map(\.info.save.sha256) == [sha("A"), sha("B"), sha("A")])
        #expect(versions.map(\.info.sequence) == [3, 2, 1])
        #expect(versionFolders(iPhone) == 4)
    }

    /// A version whose save was replaced by hand (its `.version.json`
    /// unchanged) is identical by its metadata only: it is kept, and the
    /// export gives the bytes of the version, from its identical copy.
    @Test func aVersionChangedByHandIsKeptAndTheExportUsesTheGoodCopy() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "same"), rtc: nil, modifiedAt: nil)
        let macVersion = try await vault(mac, sandbox, clock: TestClock(start: Self.later))
            .addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "same"), rtc: nil, modifiedAt: nil)
        try Data("edited".utf8).write(to: mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier/\(macVersion.id)/Game.sav"))
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))

        let merged = vault(iPhone, sandbox)
        let versions = try await merged.versions(deviceId: "dev-1", gameId: "g1")
        #expect(versions.count == 1)
        #expect(versions[0].info == macVersion.info)
        let exported = try await merged.exportVerified(deviceId: "dev-1", gameId: "g1", version: versions[0], to: sandbox.base.appending(path: "Out"))
        #expect(try Data(contentsOf: exported.save) == Data("same".utf8))
        // Nothing deleted: the edited file is still there.
        let edited = tree(iPhone).filter { $0.value == Data("edited".utf8) }
        #expect(edited.count == 1)
    }

    /// A move that fails leaves the version where it is: still listed and
    /// restorable; the next load goes on.
    @Test func aFailedMoveNeverHidesAVersion() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let mine = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let macVault = vault(mac, sandbox, clock: TestClock(start: Self.later))
        var macVersions: [SaveVersion] = []
        for content in ["m1", "m2"] {
            macVersions.append(try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", content), rtc: nil, modifiedAt: nil))
        }
        let duplicate = try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))
        // m2's folder name is taken in the canonical folder (by a folder of the user).
        let canonical = iPhone.appending(path: "My Brick/\(Self.gba)/Starlight Courier")
        try FileManager.default.createDirectory(at: canonical.appending(path: macVersions[1].id), withIntermediateDirectories: true)
        // And the canonical folder cannot be written at first.
        try FileManager.default.setAttributes([.posixPermissions: 0o555], ofItemAtPath: canonical.path)

        let locked = vault(iPhone, sandbox)
        let listed = try await locked.versions(deviceId: "dev-1", gameId: "g1")
        #expect(listed.map(\.info.save.sha256) == [sha("m2"), sha("m1"), sha("a")])
        for version in listed {
            let out = try await locked.exportVerified(deviceId: "dev-1", gameId: "g1", version: version, to: sandbox.base.appending(path: "Out"))
            #expect(try Data(contentsOf: out.save).count > 0)
        }
        #expect(try names(duplicate).count == 2)

        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: canonical.path)
        let unlocked = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        #expect(unlocked.map(\.info.save.sha256) == [sha("m2"), sha("m1"), sha("a")])
        // m1 moved; m2 stays (its name is taken), the duplicate with it.
        #expect(try names(duplicate) == [macVersions[1].id])
        #expect(FileManager.default.fileExists(atPath: canonical.appending(path: "\(macVersions[0].id)/Game.sav").path))
        #expect(FileManager.default.fileExists(atPath: canonical.appending(path: "\(mine.id)/Game.sav").path))
    }

    /// A version listed before another instance moved it is still exported.
    @Test func aListedVersionIsExportedAfterItMoved() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let macVersion = try await vault(mac, sandbox, clock: TestClock(start: Self.later))
            .addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "m"), rtc: nil, modifiedAt: nil)
        let duplicate = try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))
        // Not movable yet: one of its files is still downloading.
        let file = duplicate.appending(path: "\(macVersion.id)/Game.sav")
        let data = try evict(file)

        let screen = vault(iPhone, sandbox)
        let listed = try await screen.versions(deviceId: "dev-1", gameId: "g1")
        #expect(listed.count == 2)
        let shown = try #require(listed.first { $0.info == macVersion.info })
        #expect(shown.id.contains("/"))

        // Downloaded, then moved by another instance (a sync).
        try download(file, data)
        _ = try await vault(iPhone, sandbox).games(deviceId: "dev-1")
        #expect(!FileManager.default.fileExists(atPath: duplicate.path))
        let out = try await screen.exportVerified(deviceId: "dev-1", gameId: "g1", version: shown, to: sandbox.base.appending(path: "Out"))
        #expect(try Data(contentsOf: out.save) == Data("m".utf8))
    }

    /// What iCloud has not downloaded yet (a file deep in a game folder, a
    /// whole version folder) is not moved, and the folders holding it stay.
    @Test func placeholdersAreNeverMovedNorTheirFoldersRemoved() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        try FileManager.default.createDirectory(at: mac.appending(path: "My Brick"), withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: iPhone.appending(path: "My Brick/.console.json"), to: mac.appending(path: "My Brick/.console.json"))
        // A game only the Mac has, with a save still downloading.
        let maze = try await vault(mac, sandbox, clock: TestClock(start: Self.later))
            .addVersion(console: brick, game: game("g2", "Moonlit Maze"), save: try sandbox.input("Maze.sav", "m"), rtc: nil, modifiedAt: nil)
        let system2 = try deliver(mac.appending(path: "My Brick/\(Self.gba)"), into: iPhone.appending(path: "My Brick"))
        let file = system2.appending(path: "Moonlit Maze/\(maze.id)/Maze.sav")
        let data = try evict(file)

        let first = vault(iPhone, sandbox)
        #expect(try await first.games(deviceId: "dev-1").map(\.id).sorted() == ["g1", "g2"])
        #expect(try names(iPhone.appending(path: "My Brick")) == [Self.gba, "\(Self.gba) 2"])
        #expect(FileManager.default.fileExists(atPath: system2.appending(path: "Moonlit Maze/\(maze.id)/.Maze.sav.icloud").path))

        try download(file, data)
        _ = try await vault(iPhone, sandbox).games(deviceId: "dev-1")
        #expect(try names(iPhone.appending(path: "My Brick")) == [Self.gba])
        #expect(try names(iPhone.appending(path: "My Brick/\(Self.gba)")) == ["Moonlit Maze", "Starlight Courier"])
    }

    /// The limit, applied when a version is published, never deletes a
    /// version not downloaded.
    @Test func theRotationSkipsVersionsNotDownloaded() async throws {
        let sandbox = try VaultSandbox()
        let small = vault(sandbox.root, sandbox, maxVersions: 3)
        let courier = game("g1", "Starlight Courier")
        var added: [SaveVersion] = []
        for i in 1...3 {
            added.append(try await small.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "v\(i)"), rtc: nil, modifiedAt: nil))
        }
        let folder = sandbox.root.appending(path: "My Brick/\(Self.gba)/Starlight Courier")
        let v1 = try evict(folder.appending(path: "\(added[0].id)/Game.sav"))
        let v2 = try evict(folder.appending(path: "\(added[1].id)/Game.sav"))

        _ = try await vault(sandbox.root, sandbox, maxVersions: 3).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "v4"), rtc: nil, modifiedAt: nil)
        _ = try await vault(sandbox.root, sandbox, maxVersions: 3).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "v5"), rtc: nil, modifiedAt: nil)
        let left = try await vault(sandbox.root, sandbox, maxVersions: 3).versions(deviceId: "dev-1", gameId: "g1")
        // v1 and v2 are beyond the limit but not downloaded: kept.
        #expect(left.map(\.info.save.sha256) == [sha("v5"), sha("v4"), sha("v3"), sha("v2"), sha("v1")])

        // Once downloaded, the next version applies the limit.
        try download(folder.appending(path: "\(added[0].id)/Game.sav"), v1)
        try download(folder.appending(path: "\(added[1].id)/Game.sav"), v2)
        _ = try await vault(sandbox.root, sandbox, maxVersions: 3).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "v6"), rtc: nil, modifiedAt: nil)
        let trimmed = try await vault(sandbox.root, sandbox, maxVersions: 3).versions(deviceId: "dev-1", gameId: "g1")
        #expect(trimmed.map(\.info.save.sha256) == [sha("v6"), sha("v5"), sha("v4")])
    }

    /// Folders whose identity is not known yet are left alone, and so are
    /// the folders they could be the canonical one of.
    @Test func foldersOfUnknownIdentityBlockTheirGroup() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        try FileManager.default.createDirectory(at: mac.appending(path: "My Brick"), withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: iPhone.appending(path: "My Brick/.console.json"), to: mac.appending(path: "My Brick/.console.json"))
        let macVault = vault(mac, sandbox, clock: TestClock(start: Self.later))
        _ = try await macVault.addVersion(console: brick, game: game("g2", "Moonlit Maze"), save: try sandbox.input("Maze.sav", "m"), rtc: nil, modifiedAt: nil)
        try deliver(mac.appending(path: "My Brick/\(Self.gba)"), into: iPhone.appending(path: "My Brick"))
        let third = mac.appending(path: "Third")
        _ = try await vault(third, sandbox, clock: TestClock(start: Self.later + 100))
            .addVersion(console: brick, game: game("g3", "Sky Garden"), save: try sandbox.input("Sky.sav", "s"), rtc: nil, modifiedAt: nil)
        try deliver(third.appending(path: "My Brick/\(Self.gba)"), into: iPhone.appending(path: "My Brick"))
        // The .game.json of the plain folder's only game is still downloading.
        try evict(iPhone.appending(path: "My Brick/\(Self.gba)/Starlight Courier/.game.json"))

        let games = try await vault(iPhone, sandbox).games(deviceId: "dev-1")
        #expect(games.map(\.id).sorted() == ["g2", "g3"])
        // "GBA 2" and "GBA 3" could both belong to "GBA": nothing moved.
        #expect(try names(iPhone.appending(path: "My Brick")) == [Self.gba, "\(Self.gba) 2", "\(Self.gba) 3"])
    }

    /// A folder the vault cannot identify is never moved nor removed.
    @Test func foldersOfTheUserAreLeftAlone() async throws {
        let sandbox = try VaultSandbox()
        _ = try await vault(sandbox.root, sandbox).addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let user = sandbox.root.appending(path: "My Brick/Notes")
        try FileManager.default.createDirectory(at: user, withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: user.appending(path: "notes.txt"))
        let before = tree(sandbox.root)
        _ = try await vault(sandbox.root, sandbox).games(deviceId: "dev-1")
        #expect(tree(sandbox.root) == before)
    }

    /// A folder is removed only when empty, checked inside the removal: a
    /// version published there in the meantime keeps it.
    @Test func aFolderWithContentIsNeverRemoved() async throws {
        let sandbox = try VaultSandbox()
        let mine = vault(sandbox.root, sandbox)
        let version = try await mine.addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let folder = sandbox.root.appending(path: "My Brick/\(Self.gba)/Starlight Courier")
        try await mine.removeIfEmpty(folder, metadata: FileSaveVault.gameFile)
        #expect(FileManager.default.fileExists(atPath: folder.appending(path: "\(version.id)/Game.sav").path))
        #expect(FileManager.default.fileExists(atPath: folder.appending(path: ".game.json").path))
        // Empty but for its metadata: removed.
        let empty = sandbox.root.appending(path: "My Brick/\(Self.gba)/Empty")
        try FileManager.default.createDirectory(at: empty, withIntermediateDirectories: true)
        try Data("{}".utf8).write(to: empty.appending(path: ".game.json"))
        try await mine.removeIfEmpty(empty, metadata: FileSaveVault.gameFile)
        #expect(!FileManager.default.fileExists(atPath: empty.path))
    }

    /// Before creating a system folder, the vault looks for the folder
    /// holding that system, whatever its name.
    @Test func aNewGameGoesToTheSystemFolderFoundByIdentity() async throws {
        let sandbox = try VaultSandbox()
        _ = try await vault(sandbox.root, sandbox).addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        // Only iCloud's renamed copy is left (the user deleted the other).
        let console = sandbox.root.appending(path: "My Brick")
        try FileManager.default.moveItem(at: console.appending(path: Self.gba), to: console.appending(path: "\(Self.gba) 2"))
        _ = try await vault(sandbox.root, sandbox).addVersion(console: brick, game: game("g2", "Moonlit Maze"), save: try sandbox.input("Maze.sav", "m"), rtc: nil, modifiedAt: nil)
        #expect(try names(console) == ["\(Self.gba) 2"])
        #expect(try names(console.appending(path: "\(Self.gba) 2")) == ["Moonlit Maze", "Starlight Courier"])
    }

    @Test func theCanonicalFolderIsChosenByNameTheSameOnEveryDevice() {
        func first(_ names: [String]) -> String? { names.sorted(by: FileSaveVault.precedes).first }
        #expect(first(["Game Boy Advance (GBA) 2", "Game Boy Advance (GBA)"]) == "Game Boy Advance (GBA)")
        #expect(first(["My Brick (2)", "My Brick"]) == "My Brick")
        #expect(first(["My Brick 3", "My Brick 2"]) == "My Brick 2")
        #expect(first(["Tetris 2 2", "Tetris 2"]) == "Tetris 2")
        #expect(first(["Arcade 2", "Brick"]) == "Brick")
        #expect(first(["Renamed", "My Brick"]) == "My Brick")
        #expect(FileSaveVault.duplicateRank("X (12)").base == "X" && FileSaveVault.duplicateRank("X (12)").number == 12)
        #expect(FileSaveVault.duplicateRank("2").number == 1)
        #expect(FileSaveVault.duplicateRank(" (2)").number == 1)
        // Only numbers from 2 are duplicate suffixes.
        #expect(FileSaveVault.duplicateRank("Brick 0").number == 1 && FileSaveVault.duplicateRank("Brick (1)").number == 1)
        #expect(first(["Brick 0", "Arcade"]) == "Arcade")
    }

    // MARK: - Round 2

    /// The limit deletes only a version whose folder holds exactly its
    /// listed files, matching; the versions kept export their own bytes.
    @Test func theRotationKeepsVersionsChangedByHandOrUnreadable() async throws {
        let sandbox = try VaultSandbox()
        let courier = game("g1", "Starlight Courier")
        let small = vault(sandbox.root, sandbox, maxVersions: 2)
        var added: [SaveVersion] = []
        for i in 1...4 {
            added.append(try await small.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "v\(i)"), rtc: nil, modifiedAt: nil))
        }
        // v1 and v2 went (exactly as written); v3 and v4 are kept.
        let folder = sandbox.root.appending(path: "My Brick/\(Self.gba)/Starlight Courier")
        #expect(try names(folder) == [added[2].id, added[3].id])
        // v3 gets a file added by hand, v4 a save changed by hand.
        try Data("note".utf8).write(to: folder.appending(path: "\(added[2].id)/notes.txt"))
        try Data("edited".utf8).write(to: folder.appending(path: "\(added[3].id)/Game.sav"))
        var more: [SaveVersion] = []
        for i in 5...6 {
            more.append(try await vault(sandbox.root, sandbox, maxVersions: 2).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "v\(i)"), rtc: nil, modifiedAt: nil))
        }
        // v3 and v4 are beyond the limit but not exactly as written: kept.
        #expect(Set(try names(folder)) == [added[2].id, added[3].id, more[0].id, more[1].id])
        #expect(FileManager.default.fileExists(atPath: folder.appending(path: "\(added[2].id)/notes.txt").path))
        // A folder that cannot be read is kept too.
        let locked = folder.appending(path: more[0].id)
        try FileManager.default.setAttributes([.posixPermissions: 0o000], ofItemAtPath: locked.path)
        defer { try? FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: locked.path) }
        let v7 = try await vault(sandbox.root, sandbox, maxVersions: 2).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "v7"), rtc: nil, modifiedAt: nil)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: locked.path)
        #expect(Set(try names(folder)) == [added[2].id, added[3].id, more[0].id, more[1].id, v7.id])
        // What is left exports its own bytes; the edited copy is refused.
        let reader = vault(sandbox.root, sandbox, maxVersions: 2)
        let out = sandbox.base.appending(path: "Out")
        for (version, content) in [(v7, "v7"), (more[0], "v5"), (added[2], "v3")] {
            let copy = try await reader.exportVerified(deviceId: "dev-1", gameId: "g1", version: version, to: out)
            #expect(try Data(contentsOf: copy.save) == Data(content.utf8))
        }
        await #expect(throws: SaveVaultError.versionMismatch) {
            try await reader.export(deviceId: "dev-1", gameId: "g1", version: added[3], to: out)
        }
    }

    /// A duplicate folder is removed through the holding area; when a
    /// version arrives meanwhile, it is put back intact, with its identity.
    @Test func aFolderThatReceivesAVersionWhileHeldIsPutBack() async throws {
        let sandbox = try VaultSandbox()
        let mine = vault(sandbox.root, sandbox)
        _ = try await mine.addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let empty = sandbox.root.appending(path: "My Brick/\(Self.gba)/Starlight Courier 2")
        try FileManager.default.createDirectory(at: empty, withIntermediateDirectories: true)
        try FileManager.default.copyItem(at: sandbox.root.appending(path: "My Brick/\(Self.gba)/Starlight Courier/.game.json"), to: empty.appending(path: ".game.json"))
        await mine.setHoldingHook { held in
            try? FileManager.default.createDirectory(at: held.appending(path: "2026-11-20 10.00.00"), withIntermediateDirectories: true)
            try? Data("late".utf8).write(to: held.appending(path: "2026-11-20 10.00.00/Game.sav"))
        }
        #expect(try await mine.removeIfEmpty(empty, metadata: FileSaveVault.gameFile) == false)
        #expect(try Data(contentsOf: empty.appending(path: "2026-11-20 10.00.00/Game.sav")) == Data("late".utf8))
        #expect(FileManager.default.fileExists(atPath: empty.appending(path: ".game.json").path))
        let holding = sandbox.root.appending(path: FileSaveVault.holdingFolder)
        #expect(try FileManager.default.contentsOfDirectory(atPath: holding.path).isEmpty)

        // Without anything arriving, it goes.
        await mine.setHoldingHook(nil)
        try FileManager.default.removeItem(at: empty.appending(path: "2026-11-20 10.00.00"))
        #expect(try await mine.removeIfEmpty(empty, metadata: FileSaveVault.gameFile) == true)
        #expect(!FileManager.default.fileExists(atPath: empty.path))
        #expect(try FileManager.default.contentsOfDirectory(atPath: holding.path).isEmpty)
    }

    /// An instance that indexed the vault before another device's console
    /// folder arrived publishes after every version on disk and lists them all.
    @Test func aLoadedInstanceSeesAConsoleFolderDeliveredAfterIndexing() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let loaded = vault(iPhone, sandbox)
        _ = try await loaded.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        #expect(try await loaded.versions(deviceId: "dev-1", gameId: "g1").count == 1)
        let macVault = vault(mac, sandbox, clock: TestClock(start: Self.later))
        for content in ["m1", "m2", "m3"] {
            _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", content), rtc: nil, modifiedAt: nil)
        }
        try deliver(mac.appending(path: "My Brick"), into: iPhone)

        #expect(try await loaded.versions(deviceId: "dev-1", gameId: "g1").count == 4)
        let next = try await loaded.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "b"), rtc: nil, modifiedAt: nil)
        #expect(next.info.sequence == 4)
    }

    /// Two loaded instances: a game folder that arrives after both indexed
    /// is seen by each when it lists and publishes.
    @Test func twoLoadedInstancesSeeAGameFolderDeliveredAfterIndexing() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let first = vault(iPhone, sandbox)
        let second = vault(iPhone, sandbox, clock: TestClock(start: Self.later))
        _ = try await first.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        #expect(try await second.versions(deviceId: "dev-1", gameId: "g1").count == 1)
        // A third device's game folder arrives as "Starlight Courier 2".
        let macVault = vault(mac, sandbox, clock: TestClock(start: Self.later + 50))
        for content in ["m1", "m2"] {
            _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", content), rtc: nil, modifiedAt: nil)
        }
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))
        #expect(try await first.versions(deviceId: "dev-1", gameId: "g1").count == 3)
        let published = try await second.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "b"), rtc: nil, modifiedAt: nil)
        #expect(published.info.sequence == 3)
        #expect(try await first.versions(deviceId: "dev-1", gameId: "g1").map(\.info.sequence) == [3, 2, 1, 1])
    }

    /// A listed version whose indexed copy was changed by hand is exported
    /// from an identical copy that arrived since, under another path.
    @Test func exportFindsAGoodCopyThatArrivedAfterIndexing() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let reader = vault(iPhone, sandbox)
        let version = try await reader.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "good"), rtc: nil, modifiedAt: nil)
        let listed = try #require(try await reader.versions(deviceId: "dev-1", gameId: "g1").first)
        try Data("bad!".utf8).write(to: iPhone.appending(path: "My Brick/\(Self.gba)/Starlight Courier/\(version.id)/Game.sav"))
        _ = try await vault(mac, sandbox, clock: TestClock(start: Self.later))
            .addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "good"), rtc: nil, modifiedAt: nil)
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))
        let copy = try await reader.exportVerified(deviceId: "dev-1", gameId: "g1", version: listed, to: sandbox.base.appending(path: "Out"))
        #expect(try Data(contentsOf: copy.save) == Data("good".utf8))
    }

    /// Same sequence and sync date: the order does not change when a
    /// version moves into the canonical folder.
    @Test func theOrderOfVersionsDoesNotChangeWhenTheyMove() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let instant = Date(timeIntervalSince1970: 1_795_609_330)
        _ = try await FileSaveVault(location: .iCloud, root: iPhone, stagingDirectory: sandbox.staging, timeZone: Self.paris, now: { instant })
            .addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "iphone"), rtc: nil, modifiedAt: nil)
        _ = try await FileSaveVault(location: .iCloud, root: mac, stagingDirectory: sandbox.staging, timeZone: TimeZone(identifier: "UTC")!, now: { instant })
            .addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "mac"), rtc: nil, modifiedAt: nil)
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))
        let canonical = iPhone.appending(path: "My Brick/\(Self.gba)/Starlight Courier")
        try FileManager.default.setAttributes([.posixPermissions: 0o555], ofItemAtPath: canonical.path)
        let before = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1").map(\.info)
        try FileManager.default.setAttributes([.posixPermissions: 0o755], ofItemAtPath: canonical.path)
        let after = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1").map(\.info)
        #expect(try names(canonical).count == 2)
        #expect(before.count == 2 && before == after)
    }

    /// The first pass never moves a system folder of unknown identity, nor
    /// mixes two systems behind the same folder name.
    @Test func consoleFoldersNeverMixDifferentSystems() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        // On the Mac, a Game Boy Color game sits in a folder named like the GBA one.
        _ = try await vault(mac, sandbox, clock: TestClock(start: Self.later))
            .addVersion(console: brick, game: game("g2", "Moonlit Maze", system: "Game Boy Color (GBC)"), save: try sandbox.input("Maze.sav", "m"), rtc: nil, modifiedAt: nil)
        try FileManager.default.moveItem(at: mac.appending(path: "My Brick/Game Boy Color (GBC)"), to: mac.appending(path: "My Brick/\(Self.gba)"))
        try deliver(mac.appending(path: "My Brick"), into: iPhone)

        let merged = vault(iPhone, sandbox)
        #expect(try await merged.games(deviceId: "dev-1").map(\.id).sorted() == ["g1", "g2"])
        #expect(try names(iPhone.appending(path: "My Brick/\(Self.gba)")) == ["Starlight Courier"])
        #expect(try names(iPhone.appending(path: "My Brick 2/\(Self.gba)")) == ["Moonlit Maze"])
    }

    /// The app stopped while a duplicate that had received a version was
    /// in the holding area: the next load moves it back among its
    /// siblings, and its versions are listed again.
    @Test func aFolderLeftInTheHoldingAreaIsRecoveredAtTheNextLoad() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        _ = try await vault(mac, sandbox, clock: TestClock(start: Self.later))
            .addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "m"), rtc: nil, modifiedAt: nil)
        // As left by a crash between the move in and the put-back.
        let holding = iPhone.appending(path: FileSaveVault.holdingFolder)
        try FileManager.default.createDirectory(at: holding, withIntermediateDirectories: true)
        try FileManager.default.moveItem(at: mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), to: holding.appending(path: UUID().uuidString))
        // And a held folder whose identity cannot be read.
        let unknown = holding.appending(path: UUID().uuidString)
        try FileManager.default.createDirectory(at: unknown, withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: unknown.appending(path: "notes.txt"))

        let versions = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        #expect(versions.map(\.info.save.sha256) == [sha("m"), sha("a")])
        #expect(try FileManager.default.contentsOfDirectory(atPath: holding.path).isEmpty)
        let recovered = try names(iPhone).filter { $0.hasPrefix("Recovered ") }
        #expect(recovered.count == 1)
        let folder = try #require(recovered.first)
        #expect(try Data(contentsOf: iPhone.appending(path: "\(folder)/notes.txt")) == Data("mine".utf8))
    }
}
