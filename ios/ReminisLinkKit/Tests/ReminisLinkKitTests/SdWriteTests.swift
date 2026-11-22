import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

@Suite("SD browser writes and game rename (M23)")
struct SdWriteTests {
    @Test func decodesTheNewFieldsAndAnOlderPak() throws {
        let folder = try ProtocolJSON.decoder().decode(SdFolder.self, from: Data(#"""
        {"folderId":"se_Um9tcw","path":"Roms","parentId":null,"writable":true,"truncated":false,"entries":[
          {"sdEntryId":"se_a","name":"Hacks","kind":"folder","sizeBytes":null,"modifiedAt":"2026-11-22T10:00:00Z","deletable":true,"protection":null},
          {"sdEntryId":"se_b","name":".system","kind":"folder","sizeBytes":null,"modifiedAt":"2026-11-22T10:00:00Z","deletable":false,"protection":"system"},
          {"sdEntryId":"se_c","name":"x","kind":"file","sizeBytes":1,"modifiedAt":"2026-11-22T10:00:00Z","deletable":false,"protection":"future"}
        ]}
        """#.utf8))
        #expect(folder.writable && folder.entries[0].deletable && folder.entries[0].protection == nil)
        #expect(folder.entries[1].protection == .system && !folder.entries[1].deletable)
        #expect(folder.entries[2].protection?.rawValue == "future")
        // A pak before M23: read only.
        let old = try ProtocolJSON.decoder().decode(SdFolder.self, from: Data(#"""
        {"folderId":null,"path":"","parentId":null,"truncated":false,"entries":[
          {"sdEntryId":"se_a","name":"Roms","kind":"folder","sizeBytes":null,"modifiedAt":"2026-11-22T10:00:00Z"}]}
        """#.utf8))
        #expect(!old.writable && !old.entries[0].deletable && old.entries[0].protection == nil)
    }

    @Test func trashItemsCarryTheVersion() throws {
        let date = try #require(ProtocolJSON.parseDate("2026-11-22T10:00:00Z"))
        let file = SdEntry(sdEntryId: "se_a", name: "a", kind: .file, sizeBytes: 5, modifiedAt: date, version: "v1-x")
        let folder = SdEntry(sdEntryId: "se_b", name: "b", kind: .folder, sizeBytes: nil, modifiedAt: date)
        let json = try JSONSerialization.jsonObject(with: ProtocolJSON.encoder().encode(SdTrashRequest(entries: [file, folder, file]))) as? [String: Any]
        let items = try #require(json?["items"] as? [[String: Any]])
        #expect(items.count == 2)
        #expect(items[0]["sizeBytes"] as? Int == 5 && items[0]["version"] as? String == "v1-x" && items[0]["modifiedAt"] as? String == "2026-11-22T10:00:00Z")
        #expect(items[1]["sizeBytes"] is NSNull && items[1]["version"] as? String == "")
        #expect(!SdTrashRequest(entries: [folder]).isComplete && SdTrashRequest(entries: [file]).isComplete)
        let decoded = try ProtocolJSON.decoder().decode(SdEntry.self, from: Data(#"{"sdEntryId":"se_a","name":"a","kind":"file","sizeBytes":1,"modifiedAt":"2026-11-22T10:00:00Z","version":"v1-y"}"#.utf8))
        #expect(decoded.version == "v1-y")
    }

    @Test func encodesTheRootAsNull() throws {
        let folder = try JSONSerialization.jsonObject(with: ProtocolJSON.encoder().encode(SdFolderRequest(parentId: nil, name: "A"))) as? [String: Any]
        #expect(folder?["parentId"] is NSNull && folder?["name"] as? String == "A")
        let upload = try JSONSerialization.jsonObject(with: ProtocolJSON.encoder().encode(
            CreateUploadRequest(target: .sdFile(folderId: nil, fileName: "a.txt"), sizeBytes: 1, sha256: String(repeating: "0", count: 64))
        )) as? [String: Any]
        let target = upload?["target"] as? [String: Any]
        #expect(target?["kind"] as? String == "sd-file" && target?["folderId"] is NSNull && target?["fileName"] as? String == "a.txt")
        #expect(TransferTarget.sdFile(folderId: "se_x", fileName: "a").withFileName("b") == .sdFile(folderId: "se_x", fileName: "b"))
    }

    @Test func gameNameRules() {
        for (name, problem) in [
            ("", GameNameRule.Problem.empty), ("   ", .empty), ("a/b", .forbiddenCharacter("/")), ("a:b", .forbiddenCharacter(":")),
            ("a?b", .forbiddenCharacter("?")), ("a\u{202e}b", .invisibleCharacter), ("a\u{1}b", .invisibleCharacter),
            (".hidden", .startsWithDot), ("end ", .trailingSpaceOrDot), ("end.", .trailingSpaceOrDot),
            ("CON", .reserved), ("lpt9.txt", .reserved), (String(repeating: "é", count: 120), .tooLong),
        ] {
            #expect(GameNameRule.problem(name, fileExtension: ".gba") == problem, "\(name)")
        }
        for name in ["Sky Garden Quest (Europe)", "Pixel Garden 🌱", "Cafe\u{301}", "[b1] Hack, v1.2", "COM0", "Console"] {
            #expect(GameNameRule.problem(name, fileExtension: ".gba") == nil, "\(name)")
        }
        #expect(GameNameRule.normalized("Cafe\u{301}") == "Café")
        #expect(GameNameRule.editableName(fileName: "Sky.Garden.gba", isFolder: false) == "Sky.Garden")
        #expect(GameNameRule.keptExtension(fileName: "Sky.Garden.gba", isFolder: false) == ".gba")
        #expect(GameNameRule.editableName(fileName: "Vol. 3", isFolder: true) == "Vol. 3")
        #expect(GameNameRule.keptExtension(fileName: "Vol. 3", isFolder: true) == "")
        #expect(GameNameRule.editableName(fileName: ".hidden", isFolder: false) == ".hidden")
    }

    @Test func serverErrorDetails() {
        let protected = ServerError(status: 409, code: .conflict, message: "", details: ["protected": .string("system")])
        #expect(protected.sdProtection == .system && protected.sdFreeName == nil)
        let taken = ServerError(status: 409, code: .conflict, message: "", details: ["existing": .string("a"), "freeName": .string("a (2)")])
        #expect(taken.sdFreeName == "a (2)" && taken.sdProtection == nil)
        let rename = ServerError(status: 409, code: .conflict, message: "", details: ["conflicts": .array([.string("Saves/x.sav")])])
        #expect(rename.renameConflicts == ["Saves/x.sav"] && !rename.renameSharesDiscs)
        let shared = ServerError(status: 409, code: .conflict, message: "", details: ["conflicts": .array([]), "shared": .bool(true)])
        #expect(shared.renameSharesDiscs)
    }

    // MARK: - Against the fake pak

    private func serverError(_ operation: () async throws -> Void) async -> ServerError? {
        do {
            try await operation()
            return nil
        } catch {
            if case .server(let server)? = error as? APIError { return server }
            return nil
        }
    }

    @Test func foldersFilesAndTrashOnTheFakePak() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let root = try await client.browseSd()
        #expect(root.writable)
        let roms = try #require(root.entries.first { $0.name == "Roms" })
        #expect(!roms.deletable && roms.protection == .root)
        #expect(root.entries.first { $0.name == ".userdata" }?.protection == .system)
        let downloads = try #require(root.entries.first { $0.name == "Downloads" })
        #expect(downloads.deletable)
        let userdata = try #require(root.entries.first { $0.name == ".userdata" })
        #expect(try await client.browseSd(folderId: userdata.sdEntryId).writable == false)

        // A folder, then a file uploaded into it, twice.
        let folder = try await client.createSdFolder(SdFolderRequest(parentId: downloads.sdEntryId, name: "Notes"))
        #expect(folder.isFolder && folder.deletable)
        let taken = await serverError { _ = try await client.createSdFolder(SdFolderRequest(parentId: downloads.sdEntryId, name: "notes")) }
        #expect(taken?.code == .conflict)
        let refused = await serverError { _ = try await client.createSdFolder(SdFolderRequest(parentId: userdata.sdEntryId, name: "x")) }
        #expect(refused?.sdProtection == .system)
        let file = try makeFile(size: 300, name: "readme.txt")
        let data = try Data(contentsOf: file)
        func upload(_ onConflict: OnConflict) async throws -> UploadResult {
            let upload = ResumableUpload(
                source: UploadSource(fileURL: file, sizeBytes: Int64(data.count), sha256: sha256Hex(data)),
                target: .sdFile(folderId: folder.sdEntryId, fileName: "readme.txt"), onConflict: onConflict,
                scratchDirectory: downloadsDirectory()
            )
            return try await upload.run(client: client)
        }
        let first = try await upload(.fail)
        #expect(first.sdEntryId == FakePak.sdEntryId("Downloads/Notes/readme.txt"))
        let conflict = await serverError { _ = try await upload(.fail) }
        #expect(conflict?.sdFreeName == "readme (2).txt")
        let second = try await upload(.rename)
        #expect(second.sdEntryId == FakePak.sdEntryId("Downloads/Notes/readme (2).txt"))
        #expect(try await client.browseSd(folderId: folder.sdEntryId).entries.map(\.name) == ["readme (2).txt", "readme.txt"])

        // To the trash, and back.
        let protectedTrash = await serverError { _ = try await client.trashSdEntries(SdTrashRequest(entries: [roms])) }
        // A version the card no longer has: refused, nothing moves.
        // The folder changed when the files were added: its first listing is
        // stale (as on the pak, Codex review of M23, round 2).
        let changed = await serverError { _ = try await client.trashSdEntries(SdTrashRequest(entries: [folder])) }
        #expect(changed?.sdChanged == [folder.sdEntryId])
        // Without a version: 400.
        let bare = SdEntry(sdEntryId: folder.sdEntryId, name: folder.name, kind: .folder, sizeBytes: nil, modifiedAt: folder.modifiedAt)
        #expect(!SdTrashRequest(entries: [bare]).isComplete)
        let refusedBare = await serverError { _ = try await client.trashSdEntries(SdTrashRequest(entries: [bare])) }
        #expect(refusedBare?.code == .invalidRequest)
        #expect(protectedTrash?.sdProtection == .root)
        let listed = try #require(try await client.browseSd(folderId: downloads.sdEntryId).entries.first { $0.name == "Notes" })
        let moved = try await client.trashSdEntries(SdTrashRequest(entries: [listed]))
        #expect(moved.moved.count == 2)
        #expect(try await client.browseSd(folderId: downloads.sdEntryId).entries.contains { $0.name == "Notes" } == false)
        let entry = try #require(try await client.trash().entries.first)
        #expect(entry.reason == "delete-file" && entry.label == "Notes")
        _ = try await client.restoreTrashEntry(id: entry.id, onConflict: .fail)
        #expect(try await client.browseSd(folderId: folder.sdEntryId).entries.count == 2)
    }

    /// Codex review of M23 (round 3): in the demo, a file replaced by
    /// another of the same name and size within the same second gets
    /// another version, so the first listing cannot move the new one.
    @Test func aReplacedFileGetsAnotherVersionInTheDemo() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let downloads = FakePak.sdEntryId("Downloads")
        let file = try makeFile(size: 64, name: "same.txt")
        let data = try Data(contentsOf: file)
        func send() async throws {
            let upload = ResumableUpload(
                source: UploadSource(fileURL: file, sizeBytes: Int64(data.count), sha256: sha256Hex(data)),
                target: .sdFile(folderId: downloads, fileName: "same.txt"), onConflict: .fail,
                scratchDirectory: downloadsDirectory()
            )
            _ = try await upload.run(client: client)
        }
        try await send()
        let first = try #require(try await client.browseSd(folderId: downloads).entries.first { $0.name == "same.txt" })
        _ = try await client.trashSdEntries(SdTrashRequest(entries: [first]))
        try await send()
        let second = try #require(try await client.browseSd(folderId: downloads).entries.first { $0.name == "same.txt" })
        #expect(second.version != first.version)
        let stale = await serverError { _ = try await client.trashSdEntries(SdTrashRequest(entries: [first])) }
        #expect(stale?.sdChanged == [first.sdEntryId])
    }

    /// Codex review of M23 (round 3): the selection keeps the version of
    /// each entry; a refresh drops the replaced or gone ones.
    @Test func selectionKeepsTheVersionShown() {
        let date = Date(timeIntervalSince1970: 1_000)
        let a = SdEntry(sdEntryId: "se_a", name: "a", kind: .file, sizeBytes: 1, modifiedAt: date, version: "v2-a")
        let b = SdEntry(sdEntryId: "se_b", name: "b", kind: .file, sizeBytes: 1, modifiedAt: date, version: "v2-b")
        var selection = SdSelection()
        let addedA = selection.toggle(a)
        let addedB = selection.toggle(b)
        #expect(addedA && addedB && selection.count == 2)
        let replaced = SdEntry(sdEntryId: "se_a", name: "a", kind: .file, sizeBytes: 1, modifiedAt: date, version: "v2-other")
        let dropped = selection.prune(listing: [replaced, b])
        #expect(dropped == 1)
        #expect(selection.entries == [b])
        // The request carries the remembered version.
        #expect(SdTrashRequest(entries: selection.entries).items.map(\.version) == ["v2-b"])
        let droppedAll = selection.prune(listing: [])
        #expect(droppedAll == 1 && selection.isEmpty)
        // At most 100.
        for i in 0..<100 {
            selection.toggle(SdEntry(sdEntryId: "se_\(i)", name: "\(i)", kind: .file, sizeBytes: 1, modifiedAt: date, version: "v"))
        }
        let added101 = selection.toggle(a)
        #expect(!added101 && selection.count == 100)
        let removed = selection.toggle(SdEntry(sdEntryId: "se_0", name: "0", kind: .file, sizeBytes: 1, modifiedAt: date, version: "v"))
        #expect(removed)
        #expect(selection.count == 99)
    }

    @Test func aFileAddedToASystemFolderIsAGame() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let system = try #require(pak.library.systems.first)
        let file = try makeFile(size: 100, name: "New Game.hab")
        let data = try Data(contentsOf: file)
        let upload = ResumableUpload(
            source: UploadSource(fileURL: file, sizeBytes: Int64(data.count), sha256: sha256Hex(data)),
            target: .sdFile(folderId: FakePak.sdEntryId("Roms/" + system.folder), fileName: "New Game.hab"), onConflict: .fail,
            scratchDirectory: downloadsDirectory()
        )
        _ = try await upload.run(client: client)
        #expect(pak.library.systems.first?.games.contains { $0.path == "New Game.hab" } == true)
    }

    @Test func renamesAGameOnTheFakePak() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let system = try #require(pak.library.systems.first)
        let game = try #require(system.games.first { $0.path == "Thistledown Isles.hab" })
        let id = DemoLibrary.id(system.folder + "/" + game.path)
        let before = try await client.game(id: id)
        let result = try await client.renameGame(id: id, name: "Thistledown Isles (Europe)")
        #expect(result.game.fileName == "Thistledown Isles (Europe).hab" && result.game.id != id && result.renamed.count == 1)
        #expect(result.game.cheatCount == 12 && result.game.hasSave)
        let taken = await serverError { _ = try await client.renameGame(id: result.game.id, name: "Brambleway Dash") }
        #expect(taken?.renameConflicts == ["Roms/\(system.folder)/Brambleway Dash.hab"])
        let invalid = await serverError { _ = try await client.renameGame(id: result.game.id, name: "a:b") }
        #expect(invalid?.code == .invalidName)
        let gone = await serverError { _ = try await client.renameGame(id: id, name: "X") }
        #expect(gone?.code == .notFound)
        // Two games of one ROM file name share their data: 409 shared, as
        // on the pak (Codex review of M23, round 2).
        pak.addGameFromAnotherClient(systemFolder: system.folder, path: "Copies/Brambleway Dash.hab")
        let shared = await serverError {
            _ = try await client.renameGame(id: DemoLibrary.id(system.folder + "/Brambleway Dash.hab"), name: "Bramble")
        }
        #expect(shared?.renameSharesDiscs == true)
        #expect(before.files.isEmpty == false)
    }
}
