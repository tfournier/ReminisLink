import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

/// A multi-disc demo game.
let starlightSaga = DemoLibrary.id("Disc Console (PS)/Orrery of Nine Tides")

@Suite("Patch models")
struct PatchModelTests {
    func json(_ value: some Encodable) throws -> [String: Any] {
        try #require(JSONSerialization.jsonObject(with: ProtocolJSON.encoder().encode(value)) as? [String: Any])
    }

    @Test func encodesTheRequestOfTheContract() throws {
        let staging = try json(PatchRequest(patch: .staging(stagingId: "st_1"), output: PatchOutput(mode: .newFile, fileName: "A (FR).gba")))
        #expect(staging["patch"] as? [String: String] == ["type": "staging", "stagingId": "st_1"])
        #expect(staging["output"] as? [String: String] == ["mode": "new-file", "fileName": "A (FR).gba"])

        // `replace` never sends a file name.
        let sd = try json(PatchRequest(patch: .sd(sdFileId: "sd_2"), output: PatchOutput(mode: .replace, fileName: "ignored.gba")))
        #expect(sd["patch"] as? [String: String] == ["type": "sd", "sdFileId": "sd_2"])
        #expect(sd["output"] as? [String: String] == ["mode": "replace"])
    }

    @Test func suggestsThePatchedNameOfThePak() {
        #expect(PatchNaming.suggestedFileName(forROM: "Thistledown Isles.gba") == "Thistledown Isles (patched).gba")
        #expect(PatchNaming.suggestedFileName(forROM: "Mosslight Maze 🌙.GB") == "Mosslight Maze 🌙 (patched).GB")
        #expect(PatchNaming.suggestedFileName(forROM: "Game v1.1.sfc") == "Game v1.1 (patched).sfc")
        #expect(PatchNaming.suggestedFileName(forROM: "NoExtension") == "NoExtension (patched)")
    }

    @Test func refusesNamesThePakRefuses() {
        #expect(PatchNaming.validatedFileName("  Thistledown Isles (FR).gba ") == "Thistledown Isles (FR).gba")
        for bad in ["", "   ", ".hidden.gba", "a/b.gba", "a\\b.gba", "tab\there.gba", "CON.gba", "lpt1", "nul .gba",
                    "zero\u{200B}width.gba", String(repeating: "é", count: 128) + ".gba"] {
            #expect(PatchNaming.validatedFileName(bad) == nil, "\(bad)")
        }
        #expect(PatchNaming.validatedFileName("CONSOLE.gba") == "CONSOLE.gba")
    }

    @Test func recognizesPatchFilesAndSinglePatchShares() {
        #expect(PatchNaming.isPatchFileName("hack.BPS"))
        #expect(PatchNaming.isPatchFileName("t.ips") && PatchNaming.isPatchFileName("t.ups"))
        #expect(!PatchNaming.isPatchFileName("game.gba") && !PatchNaming.isPatchFileName("bps"))
        #expect(PatchNaming.sameExtension("a.GBA", "b.gba") && !PatchNaming.sameExtension("a.gba", "a.gb"))

        #expect(ShareRequest(files: ["hack.bps"]).isSinglePatch)
        #expect(!ShareRequest(files: ["hack.bps", "other.ips"]).isSinglePatch)
        #expect(!ShareRequest(files: ["game.gba"]).isSinglePatch)
        #expect(!ShareRequest(files: []).isSinglePatch)
    }

    @Test func onlySingleFileGamesArePatchable() {
        #expect(GameKind.file.isPatchable)
        #expect(!GameKind.m3u.isPatchable && !GameKind.folderM3U.isPatchable && !GameKind.folderCUE.isPatchable)
        #expect(!GameKind(rawValue: "something-new").isPatchable)
    }

    @Test func readsTheResultOfAJob() throws {
        let job = try ProtocolJSON.decoder().decode(Job.self, from: Data(#"""
        {"id":"job_1","kind":"patch","state":"failed","progress":{"done":0,"total":16777216},
         "createdAt":"2026-11-18T17:00:00Z","updatedAt":"2026-11-18T17:00:04Z","result":null,
         "error":{"code":"patch_mismatch","message":"Source mismatch","details":{}}}
        """#.utf8))
        #expect(job.kind == .patch)
        #expect(job.serverError?.code == .patchMismatch)
        let other = try ProtocolJSON.decoder().decode(Job.self, from: Data(#"""
        {"id":"job_2","kind":"future-kind","state":"succeeded","progress":{"done":1,"total":null},
         "createdAt":"2026-11-18T17:00:00Z","updatedAt":"2026-11-18T17:00:04Z","result":{"stagingId":"st_9"},"error":null}
        """#.utf8))
        #expect(other.kind.rawValue == "future-kind")
        #expect(other.resultStagingId == "st_9")
        #expect(other.resultGameId == nil)
    }

    @Test func demoPatchesAreWellFormed() {
        // CRC-32 check value.
        #expect(DemoPatch.crc32(Data("123456789".utf8)) == 0xCBF4_3926)
        #expect(DemoPatch.varint(0) == Data([0x80]))
        #expect(DemoPatch.varint(128) == Data([0x00, 0x80]))
        let target = Data("patched".utf8)
        let bps = DemoPatch.bps(sourceSize: 10, sourceCRC32: 0x4AFA_16BD, target: target)
        #expect(bps.prefix(4) == Data("BPS1".utf8))
        // The last 4 bytes are the CRC-32 of the rest of the patch.
        let body = bps.dropLast(4)
        let stored = bps.suffix(4).reversed().reduce(UInt32(0)) { $0 << 8 | UInt32($1) }
        #expect(stored == DemoPatch.crc32(Data(body)))
        #expect(DemoPatch.ips(offset: 1, bytes: Data([9])) == Data("PATCH".utf8) + Data([0, 0, 1, 0, 1, 9]) + Data("EOF".utf8))
    }
}

@Suite("Patch against the fake pak")
struct PatchFakePakTests {
    /// Uploads `data` to the staging area and returns its `stagingId`.
    func stage(_ data: Data, name: String, client: APIClient) async throws -> String {
        let file = try makeFile(size: 0, name: name)
        try data.write(to: file)
        let upload = ResumableUpload(
            source: UploadSource(fileURL: file, sizeBytes: Int64(data.count), sha256: try FileDigest.sha256Sync(of: file)),
            target: .staging(fileName: name), onConflict: .rename,
            scratchDirectory: FileManager.default.temporaryDirectory.appending(path: "rl-scratch-\(UUID().uuidString)"),
            retry: .immediate
        )
        return try #require(try await upload.run(client: client).stagingId)
    }

    /// Polls a job until it ends.
    func finish(_ jobId: String, client: APIClient) async throws -> Job {
        var job = try await client.job(id: jobId)
        for _ in 0..<10 where !job.state.isFinished { job = try await client.job(id: jobId) }
        return job
    }

    @Test func aMatchingBPSMakesANewGameNextToTheOriginal() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let bps = DemoPatch.bps(sourceSize: 16_777_216, sourceCRC32: 1, target: Data(count: 32))
        let stagingId = try await stage(bps, name: "translation.bps", client: client)
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(
            patch: .staging(stagingId: stagingId),
            output: PatchOutput(mode: .newFile, fileName: PatchNaming.suggestedFileName(forROM: "Thistledown Isles.gba"))
        ))
        let queued = try await client.job(id: accepted.jobId)
        #expect(queued.kind == .patch)
        let job = try await finish(accepted.jobId, client: client)
        #expect(job.state == .succeeded)
        #expect(job.progress.done == job.progress.total)
        let gameId = try #require(job.resultGameId)
        #expect(gameId == DemoLibrary.id("Handheld Advance (GBA)/Thistledown Isles (patched).gba"))
        #expect(job.resultVerified == true)
        let game = try await client.game(id: gameId)
        #expect(game.name == "Thistledown Isles (patched)")
        #expect(game.totalBytes == 32)
        // The original is still there.
        #expect(try await client.game(id: skyGarden).fileName == "Thistledown Isles.gba")

        // Same name again: the pak adds " (2)".
        let again = try await client.patchGame(gameId: skyGarden, PatchRequest(
            patch: .staging(stagingId: stagingId), output: PatchOutput(mode: .newFile)
        ))
        let second = try await finish(again.jobId, client: client)
        #expect(second.resultGameId == DemoLibrary.id("Handheld Advance (GBA)/Thistledown Isles (patched) (2).gba"))
    }

    @Test func replaceKeepsTheGameAndTrashesTheROM() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let trashBefore = try await client.trash().entries.count
        let before = try await client.game(id: skyGarden)
        let files = try await client.sdFiles(type: .patch)
        let ips = try #require(files.first { $0.fileName == "Bonus Levels.ips" })
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .replace)))
        let job = try await finish(accepted.jobId, client: client)
        #expect(job.state == .succeeded)
        #expect(job.resultGameId == skyGarden)
        // IPS carries no checksum.
        #expect(job.resultVerified == false)
        let game = try await client.game(id: skyGarden)
        #expect(game.hasSave && game.hasCheats)
        #expect(game.stateCount == before.stateCount)
        let trash = try await client.trash()
        #expect(trash.entries.count == trashBefore + 1)
        #expect(trash.entries.first?.reason == "replace-rom")
        #expect(trash.entries.first?.files == ["Roms/Handheld Advance (GBA)/Thistledown Isles.gba"])
    }

    @Test func aPatchForAnotherROMFailsWithPatchMismatch() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let files = try await client.sdFiles(type: .patch)
        #expect(files.count == 4)
        #expect(files.contains { $0.path.hasPrefix("ReminisLink/patches/") } && files.contains { $0.path.hasPrefix("Roms/") })
        let ups = try #require(files.first { $0.fileName == "Brambleway Dash (Hack).ups" })
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: ups.id), output: PatchOutput(mode: .replace)))
        let job = try await finish(accepted.jobId, client: client)
        #expect(job.state == .failed)
        #expect(job.serverError?.code == .patchMismatch)
        #expect(job.serverError?.patchMismatchReason == "source-mismatch")
        let details = try #require(job.serverError?.patchMismatchDetails)
        #expect(details.expectedSize == 4_194_304 && details.actualSize == 16_777_216)
        #expect(details.expectedCrc32 == "1234abcd" && details.actualCrc32 == nil)
        #expect(job.resultGameId == nil)
        // Nothing went to the trash.
        #expect(try await client.trash().entries.first?.reason != "replace-rom")
    }

    @Test func aFileThatIsNotAPatchIsRefusedAtOnce() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let stagingId = try await stage(Data("hello".utf8), name: "notes.bps", client: client)
        do {
            _ = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .staging(stagingId: stagingId), output: PatchOutput(mode: .newFile)))
            Issue.record("accepted")
        } catch let error as APIError {
            #expect(error.code == .unsupportedFormat)
        }
    }

    @Test func anAlreadyPatchedROMSaysWhy() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        // Declares the game's size as its target: the ROM is already patched.
        let ups = DemoPatch.upsHeader(sourceSize: 100, targetSize: 16_777_216)
        let stagingId = try await stage(ups, name: "hack.ups", client: client)
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .staging(stagingId: stagingId), output: PatchOutput(mode: .newFile)))
        let job = try await finish(accepted.jobId, client: client)
        #expect(job.serverError?.patchMismatchReason == "already-patched")
    }

    @Test func aROMThatChangesFailsWithConflict() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let files = try await client.sdFiles(type: .patch)
        let ips = try #require(files.first { $0.fileName == "Bonus Levels.ips" })
        pak.romChangesDuringNextPatch = true
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .replace)))
        let job = try await finish(accepted.jobId, client: client)
        #expect(job.serverError?.code == .conflict)
    }

    @Test func outputNamesKeepTheExtensionOfTheROM() async throws {
        #expect(PatchNaming.outputName("Sky (FR).gba", romFileName: "Sky.gba") == .valid("Sky (FR).gba"))
        #expect(PatchNaming.outputName("Sky (FR).GBA", romFileName: "Sky.gba") == .valid("Sky (FR).GBA"))
        #expect(PatchNaming.outputName(" Sky (FR) ", romFileName: "Sky.GBA") == .valid("Sky (FR).GBA"))
        // Another ending: the ROM's extension is added, never refused.
        #expect(PatchNaming.outputName("Sky (FR).gb", romFileName: "Sky.gba") == .valid("Sky (FR).gb.gba"))
        #expect(PatchNaming.outputName("Hack v1.2", romFileName: "Sky.gba") == .valid("Hack v1.2.gba"))
        #expect(PatchNaming.outputName("Hack.zip", romFileName: "Sky.gba") == .valid("Hack.zip.gba"))
        #expect(PatchNaming.outputName("a/b", romFileName: "Sky.gba") == .invalid)
        #expect(PatchNaming.outputName("Sky", romFileName: "NoExt") == .valid("Sky"))

        let pak = makePak()
        let client = try await pairedClient(pak)
        let files = try await client.sdFiles(type: .patch)
        let ips = try #require(files.first { $0.fileName == "Bonus Levels.ips" })
        // Without extension: the pak adds the ROM's.
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .newFile, fileName: "Sky Bonus")))
        #expect(try await finish(accepted.jobId, client: client).resultGameId == DemoLibrary.id("Handheld Advance (GBA)/Sky Bonus.gba"))
        let other = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .newFile, fileName: "Sky Bonus.txt")))
        #expect(try await finish(other.jobId, client: client).resultGameId == DemoLibrary.id("Handheld Advance (GBA)/Sky Bonus.txt.gba"))
    }

    @Test func archivesAndMultiDiscGamesAreNotPatchable() async throws {
        #expect(PatchNaming.refusal(kind: .file, fileName: "a.gba") == nil)
        #expect(PatchNaming.refusal(kind: .file, fileName: "a.ZIP") == .archive)
        #expect(PatchNaming.refusal(kind: .file, fileName: "a.7z") == .archive)
        #expect(PatchNaming.refusal(kind: .m3u, fileName: "a.m3u") == .multiDisc)

        let library = DemoLibrary(systems: [DemoLibrary.System(folder: "Handheld Advance (GBA)", label: "GBA", tag: "GBA", games: [
            DemoLibrary.Game(path: "Packed.zip", sizeBytes: 100, modifiedAt: Date()),
        ])])
        let pak = FakePak(host: "pak-\(UUID().uuidString.lowercased()).test", library: library)
        pak.register()
        let client = try await pairedClient(pak)
        let files = try await client.sdFiles(type: .patch)
        let ips = try #require(files.first { $0.fileName == "Bonus Levels.ips" })
        do {
            _ = try await client.patchGame(gameId: DemoLibrary.id("Handheld Advance (GBA)/Packed.zip"),
                                           PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .newFile)))
            Issue.record("accepted")
        } catch let error as APIError {
            #expect(error.code == .unsupportedFormat)
        }
    }

    @Test func aPatchJobCanBeCancelled() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let files = try await client.sdFiles(type: .patch)
        let bps = try #require(files.first { $0.fileName.hasSuffix(".bps") })
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: bps.id), output: PatchOutput(mode: .newFile)))
        // Running: it stays running with cancelRequested until it stops.
        _ = try await client.job(id: accepted.jobId)
        let cancelling = try await client.cancelJob(id: accepted.jobId)
        #expect(cancelling.state == .running && cancelling.cancelRequested == true)
        let final = try await client.job(id: accepted.jobId)
        #expect(final.state == .cancelled && final.cancelRequested == true)
        // Queued: cancelled at once.
        let queued = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: bps.id), output: PatchOutput(mode: .newFile)))
        #expect(try await client.cancelJob(id: queued.jobId).state == .cancelled)
    }

    @Test func refusedRequestsAnswerAtOnce() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let files = try await client.sdFiles(type: .patch)
        let ips = try #require(files.first { $0.fileName == "Bonus Levels.ips" })
        // A multi-disc game.
        do {
            _ = try await client.patchGame(gameId: starlightSaga, PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .newFile)))
            Issue.record("accepted")
        } catch let error as APIError {
            #expect(error.code == .unsupportedFormat)
        }
        // An unknown staging file.
        do {
            _ = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .staging(stagingId: "st_unknown"), output: PatchOutput(mode: .newFile)))
            Issue.record("accepted")
        } catch let error as APIError {
            #expect(error.code == .notFound)
        }
        // A name the pak refuses.
        do {
            _ = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .newFile, fileName: "a/b.gba")))
            Issue.record("accepted")
        } catch let error as APIError {
            #expect(error.code == .invalidName)
        }
    }

    @Test func patchJobsAreFollowedByTheQueue() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let files = try await client.sdFiles(type: .patch)
        let ips = try #require(files.first { $0.fileName == "Bonus Levels.ips" })
        let accepted = try await client.patchGame(gameId: skyGarden, PatchRequest(patch: .sd(sdFileId: ips.id), output: PatchOutput(mode: .newFile)))
        try await followInQueue(accepted.jobId, client: client)
    }

    @MainActor
    func followInQueue(_ jobId: String, client: APIClient) async throws {
        let queue = TransferQueue(scratchDirectory: FileManager.default.temporaryDirectory, client: { client })
        var finished: [String] = []
        queue.onJobFinished = { finished.append($0.id) }
        let item = queue.track(jobId: jobId, title: "Thistledown Isles", kind: .patch)
        #expect(item.kind == .patch)
        for _ in 0..<10 where !item.isFinished { await queue.refreshJobs() }
        #expect(finished == [jobId])
        #expect(item.job?.state == .succeeded)
    }
}
