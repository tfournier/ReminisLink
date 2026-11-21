import Foundation
@testable import ReminisLinkDemo
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

/// Codex review of M19: the demo reads the archives of a real pak, never
/// traps on a damaged one, and an abandoned archive is deleted.
@Suite("Game archives: review fixes (M19)")
struct ArchiveReviewTests {
    /// An archive written by the pak's own writer (deflated save, state
    /// and manifest, data descriptors).
    static let realPakArchive = Data(base64Encoded: "UEsDBBQACAAIAABQdV0AAAAAAAAAAAAAAAAeAAkAc2F2ZS9UaGlzdGxlZG93biBJc2xlcy5oYWIuc2F2VVQFAAGgawFrKk4sS1UYJUaJUWJYE4ABAFBLBwgwLntkEAAAAOgDAABQSwMEFAAIAAgAAFB1XQAAAAAAAAAAAAAAACIACQBzdGF0ZXMvMi9UaGlzdGxlZG93biBJc2xlcy5oYWIuc3QyVVQFAAGgawFrKi5JLElVGCVHyVFylBwlR8mRSQIGAFBLBwgCQo4YFgAAAAgHAABQSwMEFAAIAAgAAFB1XQAAAAAAAAAAAAAAAA0ACQBtYW5pZmVzdC5qc29uVVQFAAGgawFrjJJPb9QwEMXv+yksn9nWfxon2dtyAglxWnEAcZjY48ZaJ6lib6Cgfnc02YQWKSsh5eLnN8/vN8rvHWPcD2MHmR8YH7ELfUgx9Of9I3S4h9G2YUL+jnwTjikMPT8wOZ/tiJDRHedRJZTZS7lX8iTFQdD39TqWhstokR8YPcYYdzgFi5+hI62/xEguxviQKKfHn/kS+KI9RchU71/nE5y//O3yJiE9p4zdCR4p6MNxDbnKn6DBOF9A71qMjh3dBL3F1UbASyt+akPKEd3wo2cfU8S0mnyIt013LTSrcRy6//KdQ++oFQXzHWMvpM8nWse3eQ3Xxc3guSVzggnvN2PvEkxL9Fwi4up/VVP4he+f8/yAFEK8XrSgCkMDRW0kaGudk8pKX1uPpnLSKrBNhdqYsrAPUOi6csZoLBsljVZWSFXqslENkSwsW/UzZEz36hZCVhsINPOGIQ7026ltqGoTSjqBvlbSKI+1A6i086KEqsDS6kKWdW0fbF01tRdGF+B0hZX10DjA0jjpF6gdY993L38GAFBLBwjhdUoakwEAADoDAABQSwECFAMUAAgACAAAUHVdMC57ZBAAAADoAwAAHgAJAAAAAAAAAAAApIEAAAAAc2F2ZS9UaGlzdGxlZG93biBJc2xlcy5oYWIuc2F2VVQFAAGgawFrUEsBAhQDFAAIAAgAAFB1XQJCjhgWAAAACAcAACIACQAAAAAAAAAAAKSBZQAAAHN0YXRlcy8yL1RoaXN0bGVkb3duIElzbGVzLmhhYi5zdDJVVAUAAaBrAWtQSwECFAMUAAgACAAAUHVd4XVKGpMBAAA6AwAADQAJAAAAAAAAAAAApIHUAAAAbWFuaWZlc3QuanNvblVUBQABoGsBa1BLBQYAAAAAAwADAPIAAACrAgAAAAA=")!

    @Test func theDemoReadsAnArchiveOfARealPak() throws {
        let entries = try #require(FakeZip.read(Self.realPakArchive))
        #expect(entries["manifest.json"] != nil)
        #expect(entries.values.contains(Data(String(repeating: "save ", count: 200).utf8)))
        // Deflated by the fake pak too, and read back.
        let written = FakeZip.write([("a", Data(repeating: 7, count: 5000)), ("b", Data("short".utf8))], deflate: true)
        #expect(FakeZip.read(written)?["a"] == Data(repeating: 7, count: 5000))
    }

    /// A deflate stream that inflates to much more than announced is
    /// refused without being held whole (Codex review 2).
    @Test func anOversizedStreamIsRefused() throws {
        let big = Data(repeating: 0, count: 8 << 20)
        let packed = try (big as NSData).compressed(using: .zlib) as Data
        var zip = FakeZip.write([("a", big)], deflate: true)
        // Announce 10 bytes instead of 8 MiB (central uncompressed size).
        let end = try #require(zip.lastRange(of: Data([0x50, 0x4b, 0x05, 0x06])))
        let central = Int(zip[end.lowerBound + 16]) | Int(zip[end.lowerBound + 17]) << 8 | Int(zip[end.lowerBound + 18]) << 16
        zip.replaceSubrange((central + 24)..<(central + 28), with: Data([10, 0, 0, 0]))
        #expect(packed.count < 1 << 20)
        #expect(FakeZip.read(zip) == nil)
    }

    @Test func aDamagedArchiveNeverTraps() {
        let data = Self.realPakArchive
        for n in stride(from: 0, to: data.count, by: 3) {
            _ = FakeZip.read(data.prefix(n))
        }
        var flipped = data
        for i in stride(from: 0, to: flipped.count, by: 5) {
            flipped[i] ^= 0xFF
            _ = FakeZip.read(flipped)
            flipped[i] ^= 0xFF
        }
    }

    @Test func anImportIntoTheDemoOfARealPakArchive() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let stagingId = try await PatchFakePakTests().stage(Self.realPakArchive, name: "Thistledown Isles.reminislink.zip", client: client)
        let plan = try await client.inspectArchive(stagingId: stagingId, target: nil)
        #expect(plan.target == .game(gameId: skyGarden))
        // Abandoned: deleted at once; the probe of the routes still works.
        try await client.deleteStagedArchive(stagingId: stagingId)
        await #expect(throws: APIError.self) { try await client.inspectArchive(stagingId: stagingId, target: nil) }
        #expect(try await client.supportsGameArchives())
    }

    @Test func anOlderPakHasNoArchiveRoutes() async throws {
        let client = stubClient { _, _ in .error(404, code: "not_found", message: "No such route") }
        #expect(try await !client.supportsGameArchives())
        let other = stubClient { _, _ in FakeResponse(status: 405) }
        #expect(try await !other.supportsGameArchives())
    }
}

@Suite("Demo archive memory bound (M19 final review)")
struct DemoArchiveBoundTests {
    @Test func entriesTogetherStayUnderTheStagingLimit() {
        // Five entries of 13 MiB each (65 MiB in all, over 64 MiB).
        let entries = (0..<5).map { (name: "e\($0)", data: Data(repeating: UInt8($0), count: 13 << 20)) }
        #expect(FakeZip.read(FakeZip.write(entries)) == nil)
        #expect(FakeZip.read(FakeZip.write(Array(entries.prefix(3))))?.count == 3)
    }
}
