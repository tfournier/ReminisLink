import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// The demo game with a save, three states (slot 0 locked) and 12 cheats.
let skyGarden = DemoLibrary.id("Game Boy Advance (GBA)/Sky Garden Quest.gba")
/// A demo game with no save, no state and no cheat.
let pixelRunner = DemoLibrary.id("Game Boy Advance (GBA)/Pixel Runner.gba")

func downloadsDirectory() -> URL {
    FileManager.default.temporaryDirectory.appending(path: "rl-dl-tests-\(UUID().uuidString)")
}

@Suite("Save, state and cheat models")
struct GameDataModelTests {
    @Test func decodesTheContractExamples() throws {
        let backups = try ProtocolJSON.decoder().decode(BackupList.self, from: Data(#"""
        {"backups":[{"id":"bk_20261118T170000Z_a1b2","label":"Avant le boss final","createdAt":"2026-11-18T17:00:00Z","sizeBytes":131072,"reason":"manual"},
                    {"id":"bk_2","label":null,"createdAt":"2026-11-18T17:10:00Z","sizeBytes":1,"reason":"before-something-new"}]}
        """#.utf8))
        #expect(backups.backups.count == 2)
        #expect(backups.backups[0].reason == .manual)
        #expect(backups.backups[1].label == nil)
        #expect(backups.backups[1].reason.rawValue == "before-something-new")

        let none = try ProtocolJSON.decoder().decode(BackupResult.self, from: Data(#"{"backup":null}"#.utf8))
        #expect(none.backup == nil)

        let slots = try ProtocolJSON.decoder().decode(SlotList.self, from: Data(#"""
        {"slots":[{"slot":0,"sizeBytes":401408,"modifiedAt":"2026-11-18T16:55:00Z","hasThumbnail":true,"locked":true,"lockStatus":"restored"},
                  {"slot":1,"sizeBytes":401408,"modifiedAt":"2026-11-17T20:12:00Z","hasThumbnail":false,"locked":false,"lockStatus":null},
                  {"slot":2,"sizeBytes":5,"modifiedAt":"2026-11-17T20:12:00Z","hasThumbnail":false,"locked":true,"lockStatus":"missing"}],
         "resumeSlot":9}
        """#.utf8))
        #expect(slots.resumeSlot == 9)
        #expect(slots.slots[0].wasRestored)
        #expect(slots.slots[0].canDelete == false)
        #expect(slots.slots[1].lockStatus == nil && slots.slots[1].canDelete)
        #expect(slots.slots[2].isMissing && !slots.slots[2].canDownload)

        let noResume = try ProtocolJSON.decoder().decode(SlotList.self, from: Data(#"{"slots":[],"resumeSlot":null}"#.utf8))
        #expect(noResume.resumeSlot == nil)
    }

    @Test func slotListReplacesOneSlot() {
        let date = Date(timeIntervalSince1970: 0)
        let list = SlotList(slots: [
            StateSlot(slot: 0, sizeBytes: 1, modifiedAt: date, hasThumbnail: false, locked: false, lockStatus: nil),
            StateSlot(slot: 2, sizeBytes: 1, modifiedAt: date, hasThumbnail: false, locked: false, lockStatus: nil),
        ], resumeSlot: 9)
        let locked = StateSlot(slot: 2, sizeBytes: 1, modifiedAt: date, hasThumbnail: false, locked: true, lockStatus: .intact)
        #expect(list.replacing(locked).slots.map(\.locked) == [false, true])
        let added = list.replacing(StateSlot(slot: 1, sizeBytes: 1, modifiedAt: date, hasThumbnail: false, locked: false, lockStatus: nil))
        #expect(added.slots.map(\.slot) == [0, 1, 2])
        #expect(added.resumeSlot == 9)
    }

    @Test func cheatsKeepUnknownKeysAndNeverSendTheLocalId() throws {
        let list = try ProtocolJSON.decoder().decode(CheatList.self, from: Data(#"""
        {"cheats":[{"index":0,"description":"Infinite money","code":"82025490+FFFF","enabled":false,"extra":{"cheat0_handler":"0","cheat0_memory_search_size":"3"}},
                   {"description":"No extra","code":"1","enabled":true}],
         "etag":"\"sha256-9b1f\""}
        """#.utf8))
        #expect(list.etag == "\"sha256-9b1f\"")
        #expect(list.cheats[0].extra == ["cheat0_handler": "0", "cheat0_memory_search_size": "3"])
        #expect(list.cheats[1].extra == [:])
        let data = try ProtocolJSON.encoder().encode(CheatsUpdate(cheats: list.cheats))
        let object = try #require(JSONSerialization.jsonObject(with: data) as? [String: Any])
        let first = try #require((object["cheats"] as? [[String: Any]])?.first)
        #expect(first["id"] == nil)
        #expect(first["extra"] as? [String: String] == ["cheat0_handler": "0", "cheat0_memory_search_size": "3"])
        #expect(first["index"] as? Int == 0)
        // Equality ignores the local id.
        var copy = list.cheats[0]
        copy = Cheat(index: copy.index, description: copy.description, code: copy.code, enabled: copy.enabled, extra: copy.extra)
        #expect(copy == list.cheats[0])
        #expect(copy.id != list.cheats[0].id)
    }

    @Test func importRequestsMatchTheContract() throws {
        func json(_ request: CheatImportRequest) throws -> String {
            String(decoding: try ProtocolJSON.encoder().encode(request), as: UTF8.self)
        }
        #expect(try json(.init(source: .url("https://host.invalid/a.cht"), mode: .merge))
            == #"{"mode":"merge","source":{"type":"url","url":"https://host.invalid/a.cht"}}"#)
        #expect(try json(.init(source: .sd(sdFileId: "sd_1"), mode: .replace))
            == #"{"mode":"replace","source":{"sdFileId":"sd_1","type":"sd"}}"#)
        #expect(try json(.init(source: .inline("x"), mode: .merge)) == #"{"mode":"merge","source":{"text":"x","type":"inline"}}"#)
        #expect(try json(.init(source: .upload(uploadId: "up_1"), mode: .merge))
            == #"{"mode":"merge","source":{"type":"upload","uploadId":"up_1"}}"#)
    }

    @Test func createBackupRequestOmitsAMissingLabel() throws {
        #expect(String(decoding: try ProtocolJSON.encoder().encode(CreateBackupRequest(label: nil)), as: UTF8.self) == "{}")
        #expect(String(decoding: try ProtocolJSON.encoder().encode(CreateBackupRequest(label: "A")), as: UTF8.self) == #"{"label":"A"}"#)
    }

    @Test func backupLabelIsTrimmedAndLimitedTo80Scalars() {
        #expect(BackupLabel.normalized("   ") == nil)
        #expect(BackupLabel.normalized("  Before the boss \n") == "Before the boss")
        let long = String(repeating: "a", count: 100)
        #expect(BackupLabel.normalized(long)?.count == 80)
        // A flag is 2 scalars: never split, never over the limit.
        let flags = String(repeating: "🇫🇷", count: 41)
        let limited = BackupLabel.limited(flags)
        #expect(limited.unicodeScalars.count == 80)
        #expect(limited.count == 40)
        #expect(BackupLabel.limited("é" + String(repeating: "b", count: 79)) == "é" + String(repeating: "b", count: 79))
    }

    @Test func contentDispositionGivesASafeFileName() {
        #expect(ContentDisposition.fileName(#"attachment; filename="Sky Garden Quest.gba.sav""#) == "Sky Garden Quest.gba.sav")
        #expect(ContentDisposition.fileName("attachment; filename=game.st1") == "game.st1")
        #expect(ContentDisposition.fileName(#"attachment; filename="a;b.sav""#) == "a;b.sav")
        #expect(ContentDisposition.fileName(#"attachment; filename="x.sav"; filename*=UTF-8''Caf%C3%A9.sav"#) == "Café.sav")
        #expect(ContentDisposition.fileName(#"attachment; filename="../../etc/passwd""#) == "passwd")
        #expect(ContentDisposition.fileName(#"attachment; filename="..\..\x.sav""#) == "x.sav")
        #expect(ContentDisposition.fileName(#"attachment; filename="..""#) == nil)
        #expect(ContentDisposition.fileName(#"attachment; filename=".hidden""#) == nil)
        #expect(ContentDisposition.fileName("attachment") == nil)
        #expect(ContentDisposition.fileName(nil) == nil)
    }

    @Test func sdFilesOfTheGameComeFirst() {
        let files = [
            SdFile(id: "1", path: "Cheats/GBA/Zeta.cht", sizeBytes: 1),
            SdFile(id: "2", path: "Cheats/GBA/Sky Garden Quest.gba.cht", sizeBytes: 1),
            SdFile(id: "3", path: "Cheats/GB/Alpha.cht", sizeBytes: 1),
            SdFile(id: "4", path: "Cheats/GBA/sky garden quest (fr).cht", sizeBytes: 1),
        ]
        let sorted = SdFile.sortedForGame(files, fileName: "Sky Garden Quest.gba", name: "Sky Garden Quest")
        #expect(sorted.map(\.id) == ["4", "2", "3", "1"])
        #expect(files[1].fileName == "Sky Garden Quest.gba.cht")
    }
}

@Suite("Cheat editor")
struct CheatEditorTests {
    func editor(_ names: [String] = ["A", "B", "C", "D"]) -> CheatEditor {
        CheatEditor(CheatList(cheats: names.enumerated().map { i, name in
            Cheat(index: i, description: name, code: "\(i)", enabled: false, extra: ["cheat\(i)_handler": "\(i)"])
        }, etag: "\"e1\""))
    }

    @Test func movesLikeAListDoes() {
        var e = editor()
        e.move(fromOffsets: [0], toOffset: 4)
        #expect(e.cheats.map(\.description) == ["B", "C", "D", "A"])
        e = editor()
        e.move(fromOffsets: [3], toOffset: 0)
        #expect(e.cheats.map(\.description) == ["D", "A", "B", "C"])
        e = editor()
        e.move(fromOffsets: [0, 2], toOffset: 2)
        #expect(e.cheats.map(\.description) == ["B", "A", "C", "D"])
        e = editor()
        e.move(fromOffsets: [1], toOffset: 1)
        #expect(!e.hasChanges)
    }

    @Test func updateIsRenumberedAndKeepsExtra() {
        var e = editor()
        e.move(fromOffsets: [0], toOffset: 4)
        e.remove(atOffsets: [0])
        let ok1 = e.add(description: " New ", code: " 01+02 ", enabled: true)
        #expect(ok1)
        let body = e.update().cheats
        #expect(body.map(\.index) == [0, 1, 2, 3])
        #expect(body.map(\.description) == ["C", "D", "A", "New"])
        // Unknown keys travel back untouched, even when the cheat moved.
        #expect(body[2].extra == ["cheat0_handler": "0"])
        #expect(body[3].code == "01+02")
        #expect(body[3].extra == [:])
    }

    @Test func editsAreValidated() {
        var e = editor(["A"])
        let id = e.cheats[0].id
        let ok2 = e.add(description: "", code: "1", enabled: false)
        #expect(!ok2)
        let ok3 = e.add(description: "x", code: "  ", enabled: false)
        #expect(!ok3)
        let ok4 = e.update(id: id, description: "  ", code: "1", enabled: true)
        #expect(!ok4)
        let ok5 = e.update(id: id, description: "Line\nbreak", code: "1\n2", enabled: true)
        #expect(ok5)
        #expect(e.cheats[0].description == "Line break")
        #expect(e.cheats[0].code == "1 2")
        #expect(e.cheats[0].extra == ["cheat0_handler": "0"])
        let ok6 = e.update(id: UUID(), description: "a", code: "b", enabled: true)
        #expect(!ok6)
    }

    @Test func changesAreTrackedAndCanBeDiscarded() {
        var e = editor()
        #expect(!e.hasChanges)
        let id = e.cheats[1].id
        e.setEnabled(id: id, true)
        #expect(e.hasChanges)
        e.setEnabled(id: id, false)
        #expect(!e.hasChanges)
        e.remove(id: id)
        #expect(e.hasChanges)
        e.discardChanges()
        #expect(!e.hasChanges)
        #expect(e.cheats.count == 4)
    }

    @Test func loadingDropsLocalEditsAndTakesTheNewEtag() {
        var e = editor()
        e.remove(atOffsets: [0, 1])
        e.load(CheatList(cheats: [Cheat(index: 0, description: "Z", code: "9", enabled: true)], etag: "\"e2\""))
        #expect(e.etag == "\"e2\"")
        #expect(e.cheats.map(\.description) == ["Z"])
        #expect(!e.hasChanges)
    }

    @Test func onlyA412IsAConflict() {
        #expect(CheatEditor.isConflict(APIError.server(ServerError(status: 412, code: .conflict, message: "changed"))))
        #expect(!CheatEditor.isConflict(APIError.server(ServerError(status: 409, code: .conflict, message: "exists"))))
        #expect(!CheatEditor.isConflict(APIError.network(.timedOut)))
    }
}

@Suite("Saves, states and cheats against the fake pak")
struct GameDataAPITests {
    @Test func downloadsTheSaveToAFileNamedByThePak() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let root = downloadsDirectory()
        let file = try await client.downloadSave(gameId: skyGarden, into: root)
        #expect(file.url.lastPathComponent == "Sky Garden Quest.gba.sav")
        #expect(try FileDigest.size(of: file.url) == 131_072)
        #expect(file.directory.deletingLastPathComponent().standardizedFileURL == root.standardizedFileURL)
        await #expect(throws: APIError.self) { try await client.downloadSave(gameId: pixelRunner, into: root) }
        // A failed download leaves nothing behind.
        let left = try FileManager.default.contentsOfDirectory(atPath: root.path)
        #expect(left.count == 1)
    }

    @Test func replacingTheSaveIsSignedWithTheBodyHashAndReturnsTheCopy() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 200_000, name: "new.sav")
        let sha = try FileDigest.sha256Sync(of: file)
        let result = try await client.replaceSave(gameId: skyGarden, file: file, sha256: sha)
        #expect(result.backup?.reason == .beforeReplace)
        #expect(result.backup?.label == "Avant remplacement")
        #expect(pak.log.last { $0.method == "PUT" }?.status == 200)
        let backups = try await client.saveBackups(gameId: skyGarden)
        #expect(backups.first?.id == result.backup?.id)
        // A wrong hash is refused by the pak, like a tampered body.
        await #expect(throws: APIError.self) {
            try await client.replaceSave(gameId: skyGarden, file: file, sha256: String(repeating: "0", count: 64))
        }
        // No save before: no copy.
        let first = try await client.replaceSave(gameId: pixelRunner, file: file, sha256: sha)
        #expect(first.backup == nil)
    }

    @Test func replacingTheSaveIsSignedAgainAfterAClockSkew() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        pak.clockOffset = 1_000
        let file = try makeFile(size: 1_000, name: "new.sav")
        _ = try await client.replaceSave(gameId: skyGarden, file: file, sha256: try FileDigest.sha256Sync(of: file))
        let puts = pak.log.filter { $0.method == "PUT" }
        #expect(puts.map(\.status) == [401, 200])
        #expect(Set(puts.compactMap(\.nonce)).count == 2)
    }

    @Test func aSaveOver64MiBIsRefusedBeforeSending() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let file = try makeFile(size: 0, name: "big.sav")
        let handle = try FileHandle(forWritingTo: file)
        try handle.truncate(atOffset: UInt64(APIClient.maxSaveBodyBytes + 1))
        try handle.close()
        await #expect(throws: APIError.self) {
            try await client.replaceSave(gameId: skyGarden, file: file, sha256: String(repeating: "0", count: 64))
        }
        #expect(!pak.log.contains { $0.method == "PUT" })
    }

    @Test func backupCopiesCanBeCreatedRestoredDownloadedAndDeleted() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let created = try await client.createSaveBackup(gameId: skyGarden, label: "  " + String(repeating: "x", count: 90))
        #expect(created.reason == .manual)
        #expect(created.label?.count == 80)
        var list = try await client.saveBackups(gameId: skyGarden)
        #expect(list.first?.id == created.id)

        let restored = try await client.restoreSaveBackup(gameId: skyGarden, backupId: created.id)
        #expect(restored.backup?.reason == .beforeRestore)

        let file = try await client.downloadSaveBackup(gameId: skyGarden, backupId: created.id, into: downloadsDirectory())
        #expect(file.url.lastPathComponent.hasSuffix(".sav"))

        try await client.deleteSaveBackup(gameId: skyGarden, backupId: created.id)
        list = try await client.saveBackups(gameId: skyGarden)
        #expect(!list.contains { $0.id == created.id })
        await #expect(throws: APIError.self) { try await client.deleteSaveBackup(gameId: skyGarden, backupId: created.id) }
    }

    @Test func restoredIsReportedOnceThenIntact() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let first = try await client.states(gameId: skyGarden)
        #expect(first.slots.map(\.slot) == [0, 1, 2])
        #expect(first.slots[0].lockStatus == .restored)
        #expect(first.resumeSlot == 9)
        let second = try await client.states(gameId: skyGarden)
        #expect(second.slots[0].lockStatus == .intact)
    }

    @Test func aLockedStateOverwrittenInGameIsRestoredAtTheNextStart() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        _ = try await client.states(gameId: skyGarden)
        let locked = try await client.lockState(gameId: skyGarden, slot: 1)
        #expect(locked.locked && locked.lockStatus == .intact)
        pak.overwriteStateInGame(gameId: skyGarden, slot: 1)
        let trashBefore = try await client.trash().entries.count
        #expect(pak.restartWithMaintenance() == ["\(skyGarden)#1"])
        let after = try await client.states(gameId: skyGarden)
        #expect(after.slots[1].lockStatus == .restored)
        // The overwritten state went to the trash.
        #expect(try await client.trash().entries.count == trashBefore + 1)
        #expect(try await client.states(gameId: skyGarden).slots[1].lockStatus == .intact)
    }

    @Test func lockUnlockRestoreAndDelete() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        // Locked: refused with state_locked.
        await #expect(throws: APIError.server(ServerError(status: 409, code: .stateLocked, message: "State is locked"))) {
            try await client.deleteState(gameId: skyGarden, slot: 0)
        }
        pak.overwriteStateInGame(gameId: skyGarden, slot: 0)
        let restored = try await client.restoreLockedState(gameId: skyGarden, slot: 0)
        #expect(restored.lockStatus == .intact)
        let unlocked = try await client.unlockState(gameId: skyGarden, slot: 0)
        #expect(!unlocked.locked && unlocked.lockStatus == nil)
        let moved = try await client.deleteState(gameId: skyGarden, slot: 0)
        #expect(moved.movedBytes == 401_408)
        #expect(try await client.states(gameId: skyGarden).slots.map(\.slot) == [1, 2])
        let state = try await client.downloadState(gameId: skyGarden, slot: 1, into: downloadsDirectory())
        #expect(state.url.lastPathComponent == "Sky Garden Quest.gba.st1")
    }

    @Test func thumbnailsAreOptional() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let slots = try await client.states(gameId: skyGarden).slots
        let with = try #require(slots.first { $0.hasThumbnail })
        let without = try #require(slots.first { !$0.hasThumbnail })
        #expect(try await client.stateThumbnail(gameId: skyGarden, slot: with.slot) != nil)
        #expect(try await client.stateThumbnail(gameId: skyGarden, slot: without.slot) == nil)
    }

    @Test func resumeHistoryRestoreArchivesTheCurrentVersion() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let history = try await client.resumeHistory(gameId: skyGarden)
        #expect(history.entries.count == 3)
        let oldest = try #require(history.entries.last)
        _ = try await client.restoreResumeHistory(gameId: skyGarden, entryId: oldest.id)
        // The resume slot now holds that version; restoring another one
        // archives it first.
        let updated = try await client.restoreResumeHistory(gameId: skyGarden, entryId: history.entries[0].id)
        #expect(updated.entries.count == 4)
        #expect(updated.entries.count <= 5)
    }

    @Test func cheatsAreSavedWithIfMatchAndAConflictIsReported() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        var editor = CheatEditor(try await client.cheats(gameId: skyGarden))
        #expect(editor.cheats.count == 12)
        #expect(editor.cheats[0].extra == ["cheat0_handler": "0"])
        editor.move(fromOffsets: [0], toOffset: 12)
        editor.setEnabled(id: editor.cheats[0].id, true)
        let saved = try await client.replaceCheats(gameId: skyGarden, editor.update(), ifMatch: editor.etag)
        #expect(saved.etag != editor.etag)
        #expect(saved.cheats.last?.extra == ["cheat0_handler": "0"])
        editor.load(saved)

        // Changed on the console in the meantime: 412, never overwritten.
        pak.changeCheatsOnConsole(gameId: skyGarden)
        editor.remove(atOffsets: [0])
        do {
            _ = try await client.replaceCheats(gameId: skyGarden, editor.update(), ifMatch: editor.etag)
            Issue.record("no conflict")
        } catch {
            #expect(CheatEditor.isConflict(error))
        }
        let reloaded = try await client.cheats(gameId: skyGarden)
        #expect(reloaded.cheats.count == 13)
        #expect(reloaded.cheats.last?.description == "Added on the console")
    }

    @Test func importsFromEverySource() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        var result = try await client.importCheats(gameId: pixelRunner, .init(source: .url("https://host.invalid/game.cht"), mode: .merge))
        #expect(result == CheatImportResult(imported: 2, total: 2))

        let files = try await client.sdFiles(type: .cht)
        let file = try #require(files.first { $0.path.hasSuffix("Sky Garden Quest.gba.cht") })
        result = try await client.importCheats(gameId: pixelRunner, .init(source: .sd(sdFileId: file.id), mode: .merge))
        #expect(result == CheatImportResult(imported: 2, total: 4))

        result = try await client.importCheats(gameId: pixelRunner, .init(
            source: .inline("cheat0_desc = \"Inline\"\ncheat0_code = \"1\"\ncheat0_custom = \"kept\"\n"), mode: .replace
        ))
        #expect(result == CheatImportResult(imported: 1, total: 1))
        #expect(try await client.cheats(gameId: pixelRunner).cheats.first?.extra == ["cheat0_custom": "kept"])

        // A file sent to the staging area, imported by its upload id.
        let cht = try makeFile(size: 0, name: "pack.cht")
        try Data("cheat0_desc = \"Uploaded\"\ncheat0_code = \"2\"\n".utf8).write(to: cht)
        let upload = ResumableUpload(
            source: UploadSource(fileURL: cht, sizeBytes: try FileDigest.size(of: cht), sha256: try FileDigest.sha256Sync(of: cht)),
            target: .staging(fileName: "pack.cht"), onConflict: .rename,
            scratchDirectory: FileManager.default.temporaryDirectory.appending(path: "rl-scratch-\(UUID().uuidString)"),
            retry: .immediate
        )
        let uploaded = try await upload.run(client: client)
        #expect(uploaded.stagingId != nil)
        let uploadId = try #require(await upload.completedUploadId)
        result = try await client.importCheats(gameId: pixelRunner, .init(source: .upload(uploadId: uploadId), mode: .merge))
        #expect(result == CheatImportResult(imported: 1, total: 2))
    }

    @Test func importWithoutCheatIsUnsupported() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        await #expect(throws: APIError.server(ServerError(status: 422, code: .unsupportedFormat, message: "No cheat found"))) {
            try await client.importCheats(gameId: skyGarden, .init(source: .url("https://host.invalid/empty.cht"), mode: .replace))
        }
        // Nothing replaced.
        #expect(try await client.cheats(gameId: skyGarden).cheats.count == 12)
    }

    @Test func sdFilesNeedAType() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        #expect(try await client.sdFiles(type: .patch).isEmpty)
        #expect(pak.log.last?.query == "type=patch")
    }

    @Test func libraryCountersFollowTheChanges() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        func summary() async throws -> GameSummary {
            let systemId = DemoLibrary.id("Game Boy Advance (GBA)")
            return try #require(try await client.games(systemId: systemId).games.first { $0.id == skyGarden })
        }
        #expect(try await summary().lockedStateCount == 1)
        _ = try await client.lockState(gameId: skyGarden, slot: 2)
        #expect(try await summary().lockedStateCount == 2)
        _ = try await client.unlockState(gameId: skyGarden, slot: 0)
        _ = try await client.deleteState(gameId: skyGarden, slot: 0)
        #expect(try await summary().stateCount == 2)
        #expect(try await client.game(id: skyGarden).stateCount == 2)
    }
}

@Suite("File size")
struct FileSizeTests {
    @Test func sizeIsNotCachedByTheURL() throws {
        let file = try makeFile(size: 10)
        #expect(try FileDigest.size(of: file) == 10)
        let handle = try FileHandle(forWritingTo: file)
        try handle.truncate(atOffset: 1_000)
        try handle.close()
        #expect(try FileDigest.size(of: file) == 1_000)
        #expect(throws: (any Error).self) { try FileDigest.size(of: file.deletingLastPathComponent()) }
    }
}
