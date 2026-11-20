import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

@Suite("Pak and SD browser models (M12)")
struct PakModelTests {
    @Test func decodesTheContractExamplesAndIgnoresUnknownFields() throws {
        let list = try ProtocolJSON.decoder().decode(PakList.self, from: Data(#"""
        {"paks":[
          {"pakId":"pk_VG9vbHMvdGc1MDQwL1JlbWluaXNMaW5rLnBhaw","name":"ReminisLink","kind":"tool","path":"Tools/tg5040/ReminisLink.pak",
           "sizeBytes":9437184,"modifiedAt":"2026-11-20T08:00:00Z","version":"v1.0.0","self":true,"newField":{"a":1}},
          {"pakId":"pk_RW11cy90ZzUwNDAvTUdCQS5wYWs","name":"MGBA","kind":"emu","path":"Emus/tg5040/MGBA.pak",
           "sizeBytes":4096,"modifiedAt":"2026-11-18T17:00:00Z","version":null,"self":false},
          {"pakId":"pk_x","name":"Future","kind":"theme","modifiedAt":"2026-11-18T17:00:00Z"}
        ]}
        """#.utf8))
        #expect(list.paks.count == 3)
        #expect(list.paks[0].isSelf && list.paks[0].version == "v1.0.0" && list.paks[0].kind == .tool)
        #expect(!list.paks[1].isSelf && list.paks[1].version == nil && list.paks[1].kind == .emu)
        // Lenient: missing fields get defaults, an unknown kind is kept.
        #expect(list.paks[2].kind.rawValue == "theme" && list.paks[2].sizeBytes == 0 && !list.paks[2].isSelf)

        let folder = try ProtocolJSON.decoder().decode(SdFolder.self, from: Data(#"""
        {"folderId":"se_VG9vbHM","path":"Tools","parentId":null,"truncated":false,"extra":true,
         "entries":[{"sdEntryId":"se_VG9vbHMvdGc1MDQw","name":"tg5040","kind":"folder","sizeBytes":null,"modifiedAt":"2026-11-18T17:00:00Z"},
                    {"sdEntryId":"se_VG9vbHMvUkVBRE1FLnR4dA","name":"README.txt","kind":"file","sizeBytes":512,"modifiedAt":"2026-11-18T17:00:00Z"}]}
        """#.utf8))
        #expect(folder.folderId == "se_VG9vbHM" && folder.parentId == nil && !folder.isRoot)
        #expect(folder.entries.map(\.isFolder) == [true, false])
        #expect(folder.entries[0].sizeBytes == nil && folder.entries[1].sizeBytes == 512)

        let root = try ProtocolJSON.decoder().decode(SdFolder.self, from: Data(#"{"folderId":null,"path":"","parentId":null,"entries":[]}"#.utf8))
        #expect(root.isRoot && !root.truncated)
    }

    @Test func groupsToolsThenEmulators() {
        let date = Date(timeIntervalSince1970: 0)
        func pak(_ name: String, _ kind: PakKind) -> Pak {
            Pak(pakId: name, name: name, kind: kind, path: "", sizeBytes: 0, modifiedAt: date)
        }
        let groups = PakList.grouped([pak("PCSX", .emu), pak("clock", .tool), pak("Zed", .tool), pak("MGBA", .emu), pak("T", PakKind(rawValue: "x"))])
        #expect(groups.map(\.kind.rawValue) == ["tool", "emu", "x"])
        #expect(groups[0].paks.map(\.name) == ["clock", "Zed"])
        #expect(groups[1].paks.map(\.name) == ["MGBA", "PCSX"])
    }

    @Test func installableEntries() {
        let date = Date(timeIntervalSince1970: 0)
        #expect(SdEntry(sdEntryId: "a", name: "Clock.PAK.ZIP", kind: .file, sizeBytes: 1, modifiedAt: date).isInstallableAsPak)
        #expect(SdEntry(sdEntryId: "a", name: "Timer.pak", kind: .folder, sizeBytes: nil, modifiedAt: date).isInstallableAsPak)
        #expect(!SdEntry(sdEntryId: "a", name: "Timer.pak", kind: .file, sizeBytes: 1, modifiedAt: date).isInstallableAsPak)
        #expect(!SdEntry(sdEntryId: "a", name: "Roms", kind: .folder, sizeBytes: nil, modifiedAt: date).isInstallableAsPak)
        #expect(!SdEntry(sdEntryId: "a", name: "game.gba", kind: .file, sizeBytes: 1, modifiedAt: date).isInstallableAsPak)
    }

    @Test func encodesTheTargetsAndTheInstallRequest() throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = .sortedKeys
        let target = String(decoding: try encoder.encode(TransferTarget.pak(kind: .emu, fileName: "MGBA.zip")), as: UTF8.self)
        #expect(target == #"{"fileName":"MGBA.zip","kind":"pak","pakKind":"emu"}"#)
        let auto = String(decoding: try encoder.encode(TransferTarget.pak(fileName: nil)), as: UTF8.self)
        #expect(auto == #"{"kind":"pak","pakKind":"auto"}"#)
        let request = String(decoding: try encoder.encode(PakInstallRequest(sdEntryId: "se_x", onConflict: .replace)), as: UTF8.self)
        #expect(request == #"{"kind":"auto","onConflict":"replace","sdEntryId":"se_x"}"#)
        let result = try ProtocolJSON.decoder().decode(UploadResult.self, from: Data(#"{"jobId":"job_1"}"#.utf8))
        #expect(result.jobId == "job_1" && result.gameId == nil)
    }

    @Test func readsTheErrorsOfAnInstallation() {
        let conflict = ServerError(status: 0, code: .conflict, message: "Pak exists", details: ["existing": .string("Tools/tg5040/Clock.pak")])
        #expect(conflict.conflictingPakName == "Clock" && !conflict.isPakSelfConflict)
        let me = ServerError(status: 409, code: .conflict, message: "x", details: ["self": .bool(true)])
        #expect(me.isPakSelfConflict && me.conflictingPakName == nil)
        #expect(ServerError(status: 0, code: .unsupportedFormat, message: "Invalid pak: no launch.sh in the pak folder").pakRefusal == .noLaunchScript)
        let cases: [(String, PakRefusal)] = [
            ("Invalid pak: not a zip archive", .notAnArchive),
            ("Invalid pak: corrupt archive", .notAnArchive),
            ("Invalid pak: no .pak folder in the archive", .noPak),
            ("Not a .pak folder nor an archive", .noPak),
            ("Invalid pak: more than one pak in the archive", .severalPaks),
            ("Invalid pak: symbolic link in the archive", .unsafeEntry),
            ("Invalid pak: entry outside the pak folder", .unsafeEntry),
            ("Invalid pak: more than 10000 entries", .tooLarge),
            ("Invalid pak: more than 1073741824 bytes uncompressed", .tooLarge),
            ("Invalid pak: entry compressed more than 200 times", .tooLarge),
            ("Invalid pak: encrypted archive", .unsupportedCompression),
            ("Invalid pak: cannot name the pak (no name in pak.json nor a usable archive name)", .noName),
            ("Invalid pak: something new", .other("something new")),
        ]
        for (message, refusal) in cases {
            #expect(PakRefusal(message: message) == refusal, "\(message)")
        }
    }

    @Test func thePaksEventIsTyped() {
        #expect(PakEvent(SSEEvent(id: "3", type: "paks", data: "{}")) == .paks(EventPaks()))
        #expect(PakEvent(SSEEvent(id: "4", type: "paks", data: #"{"future":1}"#)) == .paks(EventPaks()))
        let job = Job(id: "j", kind: .pakInstall, state: .succeeded, progress: .init(done: 1, total: 1),
                      createdAt: Date(), updatedAt: Date(), result: ["pakId": .string("pk_1")])
        #expect(job.resultPakId == "pk_1")
    }
}

@Suite("Paks and SD browser against the fake pak (M12)")
struct PakAPITests {
    /// Follows a job until it ends.
    func finished(_ jobId: String, _ client: APIClient) async throws -> Job {
        for _ in 0..<20 {
            let job = try await client.job(id: jobId)
            if job.state.isFinished { return job }
        }
        throw APIError.unexpectedStatus(0)
    }

    @Test func listsThePaksWithReminisLinkMarked() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let paks = try await client.paks()
        #expect(paks.filter(\.isSelf).map(\.name) == ["ReminisLink"])
        #expect(paks.map(\.kind) == paks.sorted { $0.kind == .tool && $1.kind != .tool }.map(\.kind))
    }

    @Test func deletesAPakToTheTrashButNeverItself() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let paks = try await client.paks()
        let clock = try #require(paks.first { $0.name == "Clock" })
        let moved = try await client.deletePak(id: clock.pakId)
        #expect(moved.movedBytes == clock.sizeBytes)
        #expect(try await client.paks().contains { $0.name == "Clock" } == false)
        let entry = try #require(try await client.trash().entries.first)
        #expect(entry.reason == "delete-pak" && entry.label == "Clock")

        let me = try #require(paks.first { $0.isSelf })
        await #expect { try await client.deletePak(id: me.pakId) } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 409 && e.isPakSelfConflict
        }
        #expect(try await client.paks().contains(where: \.isSelf))
    }

    @Test func aBusyConsoleRefusesTheDeletion() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let clock = try #require(try await client.paks().first { $0.name == "Clock" })
        pak.busyNextWrites(1)
        await #expect { try await client.deletePak(id: clock.pakId) } throws: { ($0 as? APIError)?.isDeviceBusy == true }
        #expect(pak.installedPakNames.contains("Clock"))
    }

    @Test func installsAnUploadedArchiveThenMeetsTheConflictThenReplaces() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let scratch = downloadsDirectory()
        let file = try makeFile(size: 3000, name: "Weather.pak.zip")
        func send(_ name: String, _ onConflict: OnConflict) async throws -> Job {
            let data = try Data(contentsOf: file)
            let upload = ResumableUpload(
                source: UploadSource(fileURL: file, sizeBytes: Int64(data.count), sha256: sha256Hex(data)),
                target: .pak(fileName: name), onConflict: onConflict, scratchDirectory: scratch
            )
            let result = try await upload.run(client: client)
            return try await finished(try #require(result.jobId), client)
        }
        let first = try await send("Weather.pak.zip", .fail)
        #expect(first.kind == .pakInstall && first.state == .succeeded && first.resultPakId != nil)
        #expect(pak.installedPak(named: "Weather")?.kind == "tool")

        let again = try await send("Weather.pak.zip", .fail)
        #expect(again.state == .failed)
        #expect(again.serverError?.conflictingPakName == "Weather")

        let replaced = try await send("Weather.pak.zip", .overwrite)
        #expect(replaced.state == .succeeded)
        #expect(try await client.trash().entries.first?.reason == "replace-pak")

        let broken = try await send("broken.zip", .fail)
        #expect(broken.serverError?.pakRefusal == .notAnArchive)
        let me = try await send("ReminisLink.zip", .overwrite)
        #expect(me.serverError?.isPakSelfConflict == true)
    }

    @Test func aPakUploadRefusesRename() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let request = CreateUploadRequest(target: .pak(fileName: "a.zip"), sizeBytes: 10, sha256: String(repeating: "0", count: 64), onConflict: .rename)
        await #expect { try await client.createUpload(request) } throws: { ($0 as? APIError)?.code == .invalidRequest }
    }

    @Test func installsADownloadedArchive() async throws {
        let pak = makePak()
        pak.downloadSize = 4096
        let client = try await pairedClient(pak)
        let accepted = try await client.createDownload(DownloadRequest(
            url: "https://example.invalid/paks/Notes.pak.zip", target: .pak(kind: .emu, fileName: nil), onConflict: .fail
        ))
        let job = try await finished(accepted.jobId, client)
        #expect(job.kind == .download && job.state == .succeeded && job.resultPakId != nil)
        #expect(pak.installedPak(named: "Notes")?.kind == "emu")
        let conflict = try await finished(try await client.createDownload(DownloadRequest(
            url: "https://example.invalid/paks/Notes.pak.zip", target: .pak(kind: .emu, fileName: nil), onConflict: .fail
        )).jobId, client)
        #expect(conflict.serverError?.conflictingPakName == "Notes")
    }

    @Test func browsesTheCardWithoutTheDataOfReminisLink() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let root = try await client.browseSd()
        #expect(root.isRoot && root.parentId == nil)
        #expect(root.entries.first?.isFolder == true)
        #expect(root.entries.map(\.name).contains("Downloads"))
        let userdata = try #require(root.entries.first { $0.name == ".userdata" })
        let shared = try #require(try await client.browseSd(folderId: userdata.sdEntryId).entries.first { $0.name == "shared" })
        let inShared = try await client.browseSd(folderId: shared.sdEntryId)
        #expect(!inShared.entries.contains { $0.name.lowercased() == "reminislink" })
        #expect(inShared.parentId == userdata.sdEntryId)
        await #expect { try await client.browseSd(folderId: FakePak.sdEntryId(".userdata/shared/ReminisLink")) } throws: {
            ($0 as? APIError)?.code == .notFound
        }
        await #expect { try await client.browseSd(folderId: "se_" + Base64URL.encode(Data("../etc".utf8))) } throws: {
            ($0 as? APIError)?.code == .invalidId
        }

        let downloads = try await client.browseSd(folderId: FakePak.sdEntryId("Downloads"))
        #expect(downloads.entries.first?.name == "Timer.pak")
        let readme = try #require(downloads.entries.first { $0.name == "README.txt" })
        let file = try await client.downloadSdFile(id: readme.sdEntryId, into: downloadsDirectory())
        #expect(file.url.lastPathComponent == "README.txt")
        #expect(try Data(contentsOf: file.url).count == 512)
        await #expect { try await client.downloadSdFile(id: FakePak.sdEntryId("Downloads"), into: downloadsDirectory()) } throws: {
            ($0 as? APIError)?.code == .notFound
        }
    }

    @Test func installsFromTheCard() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let folder = try await finished(try await client.installPak(PakInstallRequest(sdEntryId: FakePak.sdEntryId("Downloads/Timer.pak"))).jobId, client)
        #expect(folder.kind == .pakInstall && folder.state == .succeeded)
        #expect(pak.installedPak(named: "Timer")?.kind == "tool")

        let clock = try await finished(try await client.installPak(PakInstallRequest(sdEntryId: FakePak.sdEntryId("Downloads/Clock.pak.zip"))).jobId, client)
        #expect(clock.serverError?.conflictingPakName == "Clock")
        let replaced = try await finished(try await client.installPak(
            PakInstallRequest(sdEntryId: FakePak.sdEntryId("Downloads/Clock.pak.zip"), onConflict: .replace)
        ).jobId, client)
        #expect(replaced.state == .succeeded && pak.installedPak(named: "Clock")?.version == "1.0")

        let noLaunch = try await finished(try await client.installPak(PakInstallRequest(sdEntryId: FakePak.sdEntryId("Downloads/NoLaunch.zip"))).jobId, client)
        #expect(noLaunch.serverError?.pakRefusal == .noLaunchScript)

        await #expect { try await client.installPak(PakInstallRequest(sdEntryId: FakePak.sdEntryId("Roms"))) } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 422 && e.pakRefusal == .noPak
        }
    }

    @Test func anOlderPakHasNoPaks() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        pak.paksSupported = false
        await #expect { try await client.paks() } throws: { ($0 as? APIError)?.code == .notFound }
    }

    @Test func installingAnnouncesPaks() async throws {
        let pak = makePak()
        let watcher = try await pairedClient(pak)
        let other = try await pairedClient(pak)
        let bytes = try await watcher.openEvents()
        let clock = try #require(try await other.paks().first { $0.name == "Clock" })
        _ = try await other.deletePak(id: clock.pakId)
        var parser = SSEParser()
        var found: PakEvent?
        while let byte = try await bytes.next() {
            if case .event(let event)? = parser.feed(byte), event.type == "paks" {
                found = PakEvent(event)
                break
            }
        }
        #expect(found == .paks(EventPaks()))
    }
}
