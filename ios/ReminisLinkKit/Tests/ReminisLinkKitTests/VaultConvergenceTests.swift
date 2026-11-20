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

    /// Every item under `root` (hidden ones included), with the content of files.
    func tree(_ root: URL) -> [String: Data] {
        var result: [String: Data] = [:]
        let manager = FileManager.default
        for case let path as String in manager.enumerator(atPath: root.path) ?? NSEnumerator() {
            let url = root.appending(path: path)
            result[path] = FileSaveVault.isDirectory(url) ? Data() : (try? Data(contentsOf: url)) ?? Data("?".utf8)
        }
        return result
    }

    func names(_ folder: URL) throws -> [String] {
        try FileManager.default.contentsOfDirectory(atPath: folder.path).filter { !$0.hasPrefix(".") }.sorted()
    }

    /// The bug seen on 2026-11-20: the iPhone and a Mac synced the same
    /// console minutes apart; iCloud had delivered the console folder but
    /// not yet the system folder, so both created "Game Boy Advance (GBA)"
    /// and iCloud renamed one "Game Boy Advance (GBA) 2".
    @Test func twoDevicesSyncingTheSameConsoleConvergeAndTheNextSyncIsANoOp() async throws {
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
        let macClock = TestClock(start: Date(timeIntervalSince1970: 1_795_609_600))
        _ = try await SaveSync(console: brick, source: source, vault: vault(mac, sandbox, clock: macClock), scratchDirectory: scratch).run()
        try deliver(mac.appending(path: "My Brick/\(Self.gba)"), into: iPhone.appending(path: "My Brick"))
        #expect(try names(iPhone.appending(path: "My Brick")) == [Self.gba, "\(Self.gba) 2"])

        let merged = vault(iPhone, sandbox)
        let games = try await merged.games(deviceId: "dev-1")
        #expect(try names(iPhone.appending(path: "My Brick")) == [Self.gba])
        #expect(games.map(\.id).sorted() == ["g1", "g2", "g3"])
        // The same save synced by both devices is one version.
        #expect(games.allSatisfy { $0.versionCount == 1 && $0.folderPath.hasPrefix("\(Self.gba)/") })
        #expect(try names(iPhone.appending(path: "My Brick/\(Self.gba)")) == ["Moonlit Maze", "Sky Garden", "Starlight Courier"])

        // The next sync stores nothing and changes nothing.
        let before = tree(iPhone)
        let report = try await SaveSync(console: brick, source: source, vault: vault(iPhone, sandbox), scratchDirectory: scratch).run()
        #expect(report.stored.isEmpty && report.unchanged == 3)
        #expect(tree(iPhone) == before)
    }

    /// Both devices created the console folder; versions are split
    /// between "My Brick" and "My Brick 2".
    @Test func duplicateConsoleFoldersAreMerged() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let a = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let macVault = vault(mac, sandbox, clock: TestClock(start: Date(timeIntervalSince1970: 1_795_700_000)))
        _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let b = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "b"), rtc: nil, modifiedAt: nil)
        _ = try await macVault.addVersion(console: brick, game: game("g2", "Moonlit Maze"), save: try sandbox.input("Maze.sav", "m"), rtc: nil, modifiedAt: nil)
        try deliver(mac.appending(path: "My Brick"), into: iPhone)
        #expect(try names(iPhone) == ["My Brick", "My Brick 2"])

        let merged = vault(iPhone, sandbox)
        #expect(try await merged.consoles().map(\.folderName) == ["My Brick"])
        #expect(try names(iPhone) == ["My Brick"])
        let versions = try await merged.versions(deviceId: "dev-1", gameId: "g1")
        #expect(versions.map(\.info.save.sha256) == [b.info.save.sha256, a.info.save.sha256])
        #expect(versions.last?.id == a.id)
        #expect(try await merged.versions(deviceId: "dev-1", gameId: "g2").count == 1)
        #expect(try names(iPhone.appending(path: "My Brick/\(Self.gba)")) == ["Moonlit Maze", "Starlight Courier"])

        // A second load changes nothing.
        let before = tree(iPhone)
        let again = vault(iPhone, sandbox)
        _ = try await again.consoles()
        _ = try await again.games(deviceId: "dev-1")
        try await again.finishRotations(deviceId: "dev-1")
        #expect(tree(iPhone) == before)
    }

    /// Game folders "X" and "X 2" with 7 versions each: merged, then the
    /// 10 newest kept (by sequence, then sync date).
    @Test func splitVersionsAreMergedThenTheLimitApplies() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let mine = vault(iPhone, sandbox)
        for i in 1...7 {
            _ = try await mine.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "iphone-\(i)"), rtc: nil, modifiedAt: nil)
        }
        let macVault = vault(mac, sandbox, clock: TestClock(start: Date(timeIntervalSince1970: 1_795_700_000)))
        for i in 1...7 {
            _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "mac-\(i)"), rtc: nil, modifiedAt: nil)
        }
        let system = iPhone.appending(path: "My Brick/\(Self.gba)")
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: system)
        #expect(try names(system) == ["Starlight Courier", "Starlight Courier 2"])

        let merged = vault(iPhone, sandbox)
        let versions = try await merged.versions(deviceId: "dev-1", gameId: "g1")
        #expect(try names(system) == ["Starlight Courier"])
        #expect(versions.count == 10)
        #expect(versions.map(\.info.sequence) == [7, 7, 6, 6, 5, 5, 4, 4, 3, 3])
        // Same sequence: the later sync first (the Mac synced later).
        #expect(versions.prefix(2).map(\.info.save.sha256) == [sha256Hex(Data("mac-7".utf8)), sha256Hex(Data("iphone-7".utf8))])
        #expect(try names(system.appending(path: "Starlight Courier")).count == 10)

        // A new version is numbered after every merged one.
        let next = try await merged.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "new"), rtc: nil, modifiedAt: nil)
        #expect(next.info.sequence == 8)
    }

    /// Each version of the canonical folder absorbs at most one identical
    /// duplicate: a save restored to an older content stays a version.
    @Test func identicalVersionsAreDroppedOneForOne() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "s1"), rtc: try sandbox.input("Game.rtc", "c"), modifiedAt: nil)
        let macVault = vault(mac, sandbox, clock: TestClock(start: Date(timeIntervalSince1970: 1_795_700_000)))
        for content in ["s1", "s2", "s1"] {
            _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", content), rtc: try sandbox.input("Game.rtc", "c"), modifiedAt: nil)
        }
        // The same save without its clock file is another version.
        _ = try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "s1"), rtc: nil, modifiedAt: nil)
        try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))

        let versions = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        let s1 = sha256Hex(Data("s1".utf8))
        let s2 = sha256Hex(Data("s2".utf8))
        #expect(versions.map(\.info.save.sha256) == [s1, s1, s2, s1])
        #expect(versions.map(\.info.sequence) == [4, 3, 2, 1])
        #expect(versions.map { $0.info.rtc != nil } == [false, true, true, true])
    }

    /// What iCloud has not downloaded yet is neither moved nor deleted;
    /// the next load finishes the merge.
    @Test func placeholdersAreLeftUntilDownloaded() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        _ = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "base"), rtc: nil, modifiedAt: nil)
        let macVault = vault(mac, sandbox, clock: TestClock(start: Date(timeIntervalSince1970: 1_795_700_000)))
        var macVersions: [SaveVersion] = []
        for content in ["m1", "m2", "m3"] {
            macVersions.append(try await macVault.addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", content), rtc: nil, modifiedAt: nil))
        }
        let duplicate = try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))
        let manager = FileManager.default
        // m2: its save is not downloaded yet.
        let m2Save = duplicate.appending(path: "\(macVersions[1].id)/Game.sav")
        let m2Data = try Data(contentsOf: m2Save)
        try manager.removeItem(at: m2Save)
        try Data().write(to: duplicate.appending(path: "\(macVersions[1].id)/.Game.sav.icloud"))
        // m3: the whole version folder is a placeholder.
        let m3 = duplicate.appending(path: macVersions[2].id)
        let parked = sandbox.base.appending(path: "Parked")
        try manager.moveItem(at: m3, to: parked)
        try Data().write(to: duplicate.appending(path: ".\(macVersions[2].id).icloud"))

        let first = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        #expect(first.map(\.info.save.sha256) == [sha256Hex(Data("m1".utf8)), sha256Hex(Data("base".utf8))])
        // The duplicate stays, with what is still downloading.
        #expect(manager.fileExists(atPath: duplicate.appending(path: "\(macVersions[1].id)/.Game.sav.icloud").path))
        #expect(manager.fileExists(atPath: duplicate.appending(path: ".\(macVersions[2].id).icloud").path))
        #expect(try names(duplicate) == [macVersions[1].id])

        // iCloud downloads them.
        try manager.removeItem(at: duplicate.appending(path: "\(macVersions[1].id)/.Game.sav.icloud"))
        try m2Data.write(to: m2Save)
        try manager.removeItem(at: duplicate.appending(path: ".\(macVersions[2].id).icloud"))
        try manager.moveItem(at: parked, to: m3)

        let second = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        #expect(second.map(\.info.save.sha256) == ["m3", "m2", "m1", "base"].map { sha256Hex(Data($0.utf8)) })
        #expect(!manager.fileExists(atPath: duplicate.path))
    }

    /// A version whose kept twin is a placeholder is not dropped yet.
    @Test func aDuplicateIsNotDroppedAgainstATwinNotDownloaded() async throws {
        let sandbox = try VaultSandbox()
        let iPhone = sandbox.root
        let mac = sandbox.base.appending(path: "Mac", directoryHint: .isDirectory)
        let courier = game("g1", "Starlight Courier")
        let kept = try await vault(iPhone, sandbox).addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "same"), rtc: nil, modifiedAt: nil)
        _ = try await vault(mac, sandbox, clock: TestClock(start: Date(timeIntervalSince1970: 1_795_700_000)))
            .addVersion(console: brick, game: courier, save: try sandbox.input("Game.sav", "same"), rtc: nil, modifiedAt: nil)
        let duplicate = try deliver(mac.appending(path: "My Brick/\(Self.gba)/Starlight Courier"), into: iPhone.appending(path: "My Brick/\(Self.gba)"))
        let keptSave = iPhone.appending(path: "My Brick/\(Self.gba)/Starlight Courier/\(kept.id)/Game.sav")
        try FileManager.default.removeItem(at: keptSave)
        try Data().write(to: keptSave.deletingLastPathComponent().appending(path: ".Game.sav.icloud"))

        _ = try await vault(iPhone, sandbox).versions(deviceId: "dev-1", gameId: "g1")
        #expect(try names(duplicate).count == 1)
    }

    /// The vault never merges nor deletes a folder it cannot identify.
    @Test func foldersWithoutIdentityAreLeftAlone() async throws {
        let sandbox = try VaultSandbox()
        let mine = vault(sandbox.root, sandbox)
        _ = try await mine.addVersion(console: brick, game: game("g1", "Starlight Courier"), save: try sandbox.input("Game.sav", "a"), rtc: nil, modifiedAt: nil)
        let user = sandbox.root.appending(path: "My Brick/\(Self.gba) 2")
        try FileManager.default.createDirectory(at: user.appending(path: "Notes"), withIntermediateDirectories: true)
        try Data("mine".utf8).write(to: user.appending(path: "Notes/notes.txt"))
        _ = try await vault(sandbox.root, sandbox).games(deviceId: "dev-1")
        #expect(try names(sandbox.root.appending(path: "My Brick")) == [Self.gba, "\(Self.gba) 2"])
        #expect(FileManager.default.fileExists(atPath: user.appending(path: "Notes/notes.txt").path))
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

    @Test func theCanonicalFolderIsTheSameOnEveryDevice() {
        func first(_ names: [String]) -> String? { names.sorted(by: FileSaveVault.precedes).first }
        #expect(first(["Game Boy Advance (GBA) 2", "Game Boy Advance (GBA)"]) == "Game Boy Advance (GBA)")
        #expect(first(["My Brick (2)", "My Brick"]) == "My Brick")
        #expect(first(["My Brick 3", "My Brick 2"]) == "My Brick 2")
        #expect(first(["Tetris 2 2", "Tetris 2"]) == "Tetris 2")
        #expect(first(["Renamed", "My Brick"]) == "My Brick")
        #expect(FileSaveVault.duplicateRank("X (12)").base == "X" && FileSaveVault.duplicateRank("X (12)").number == 12)
        #expect(FileSaveVault.duplicateRank("2").base == "2")
        #expect(FileSaveVault.duplicateRank(" (2)").base == " (2)")
    }
}
