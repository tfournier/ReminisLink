import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

@Suite("Game archive models (M19)")
struct ArchiveModelTests {
    @Test func decodesThePlanOfTheContract() throws {
        let json = """
        {"manifest":{"format":"reminislink-game-archive","version":1,"createdAt":"2026-11-21T10:00:00Z",
          "source":{"deviceName":"Brick","os":"nextui","platform":"tg5040","pakVersion":"1.1.0","systemTag":"GBA",
                    "systemLabel":"Game Boy Advance","gameName":"Sky","fileName":"Sky.gba","romName":"Sky.gba","kind":"file",
                    "future":"ignored"},
          "files":[{"path":"save/Sky.gba.sav","role":"save","sizeBytes":3,"sha256":"aa"},
                   {"path":"states/0/x","role":"state","slot":0,"sizeBytes":4,"sha256":"bb"},
                   {"path":"x/y","role":"config-of-tomorrow","sizeBytes":1,"sha256":"cc"}]},
         "target":{"gameId":"g1"},"targetName":"Sky","suggested":true,
         "entries":[{"role":"save","slot":null,"sizeBytes":3,"action":"replace","reason":null},
                    {"role":"state","slot":0,"sizeBytes":4,"action":"skip","reason":"locked"},
                    {"role":"config-of-tomorrow","slot":null,"sizeBytes":1,"action":"skip","reason":"something-new"}],
         "importBytes":3}
        """
        let plan = try ProtocolJSON.decoder().decode(ArchivePlan.self, from: Data(json.utf8))
        #expect(plan.target == .game(gameId: "g1") && plan.suggested)
        #expect(plan.replaced.count == 1 && plan.skipped.count == 2 && plan.importsAnything)
        #expect(plan.entries[2].reason == ArchiveSkipReason(rawValue: "something-new"))
        #expect(!plan.manifest.hasROM)

        let newGame = try ProtocolJSON.decoder().decode(ArchiveTarget.self, from: Data(#"{"systemId":"s","folderId":null}"#.utf8))
        #expect(newGame == .newGame(systemId: "s", folderId: nil))
        let body = try JSONSerialization.jsonObject(with: ProtocolJSON.encoder().encode(ArchiveImportRequest(target: newGame))) as? [String: Any]
        #expect((body?["target"] as? [String: String]) == ["systemId": "s"])
    }

    @Test func readsTheJobResult() throws {
        let result: [String: JSONValue] = [
            "gameId": .string("g1"), "imported": .integer(4),
            "skipped": .array([.object(["role": .string("state"), "slot": .integer(1), "reason": .string("locked")])]),
            "trashEntryId": .string("tr_1"), "saveBackupId": .null,
        ]
        let parsed = try #require(ArchiveImportResult(result))
        #expect(parsed.gameId == "g1" && parsed.imported == 4 && parsed.trashEntryId == "tr_1" && parsed.saveBackupId == nil)
        #expect(parsed.skipped.first?.slot == 1 && parsed.skipped.first?.reason == .locked)
        #expect(ArchiveImportResult(["pakId": .string("p")]) == nil)
        #expect(GameArchiveManifest.isArchiveName("Sky.REMINISLINK.zip") && !GameArchiveManifest.isArchiveName("Sky.zip"))
    }
}

@Suite("Game archives against the fake pak (M19)")
struct ArchiveFakePakTests {
    let fake = PatchFakePakTests()

    @Test func aGameMovesFromOneConsoleToAnotherThroughAnArchive() async throws {
        let source = makePak()
        let from = try await pairedClient(source)
        let folder = FileManager.default.temporaryDirectory.appending(path: "rl-archive-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: folder) }
        let archive = try await from.downloadGameArchive(gameId: skyGarden, includeROM: true, into: folder)
        #expect(archive.url.lastPathComponent == "Thistledown Isles.reminislink.zip")

        // The other console does not have the game: the archive makes it.
        var library = DemoLibrary.sample
        library.systems[0].games.removeAll { $0.path == "Thistledown Isles.hab" }
        let target = FakePak(host: "pak-\(UUID().uuidString.lowercased()).test", library: library)
        target.register()
        let to = try await pairedClient(target)
        let stagingId = try await fake.stage(try Data(contentsOf: archive.url), name: archive.url.lastPathComponent, client: to)
        let plan = try await to.inspectArchive(stagingId: stagingId, target: nil)
        #expect(plan.suggested && plan.manifest.hasROM)
        guard case .newGame(let systemId, _) = plan.target else { Issue.record("target \(String(describing: plan.target))"); return }
        #expect(plan.entries.allSatisfy { $0.action == .create || ($0.action == .skip && $0.reason == .locked) })

        let accepted = try await to.importArchive(stagingId: stagingId, target: .newGame(systemId: systemId, folderId: nil))
        let job = try await fake.finish(accepted.jobId, client: to)
        #expect(job.state == .succeeded && job.kind == .archiveImport)
        let result = try #require(ArchiveImportResult(job.result))
        #expect(result.gameId == skyGarden && result.imported > 0)
        let detail = try await to.game(id: skyGarden)
        #expect(detail.files.contains { $0.role == .save })

        // The same archive again into the game now there: a game target,
        // whose save is backed up before it is replaced.
        let again = try await fake.stage(try Data(contentsOf: archive.url), name: archive.url.lastPathComponent, client: to)
        let second = try await to.inspectArchive(stagingId: again, target: nil)
        #expect(second.target == .game(gameId: skyGarden))
        #expect(second.entries.first { $0.role == .rom }?.reason == .gameExists)
        #expect(second.entries.first { $0.role == .save }?.action == .replace)
        let job2 = try await fake.finish(try await to.importArchive(stagingId: again, target: .game(gameId: skyGarden)).jobId, client: to)
        let result2 = try #require(ArchiveImportResult(job2.result))
        #expect(result2.saveBackupId != nil && result2.trashEntryId != nil)
        #expect(try await to.saveBackups(gameId: skyGarden).first?.reason == .beforeImport)

        // A new game whose ROM is taken: 409.
        let third = try await fake.stage(try Data(contentsOf: archive.url), name: archive.url.lastPathComponent, client: to)
        await #expect(throws: APIError.self) { try await to.inspectArchive(stagingId: third, target: .newGame(systemId: systemId, folderId: nil)) }
    }

    @Test func aFileThatIsNotAnArchiveIsRefused() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let stagingId = try await fake.stage(Data("not a zip".utf8), name: "x.reminislink.zip", client: client)
        do {
            _ = try await client.inspectArchive(stagingId: stagingId, target: nil)
            Issue.record("accepted")
        } catch let error as APIError {
            #expect(error.code == .unsupportedFormat)
        }
    }

    @Test func theStateListingMatchesTheDownloads() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let list = try await client.allStates()
        let first = try #require(list.states.first { $0.gameId == skyGarden && $0.slot == 0 })
        let folder = FileManager.default.temporaryDirectory.appending(path: "rl-states-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: folder) }
        let state = try await client.downloadState(gameId: skyGarden, slot: 0, into: folder)
        #expect(try FileDigest.sha256Sync(of: state.url) == first.sha256)
        if let thumbnail = first.thumbnail {
            let file = try await client.downloadStateThumbnail(gameId: skyGarden, slot: 0, into: folder)
            #expect(try FileDigest.sha256Sync(of: file.url) == thumbnail.sha256)
        }
    }
}
