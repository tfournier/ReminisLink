import Foundation
import ReminisLinkDemo
import Testing
@testable import ReminisLinkKit

@Suite("Restore from the trash: models (M18)")
struct TrashRestoreModelTests {
    @Test func decodesTheContractExamples() throws {
        let preview = try ProtocolJSON.decoder().decode(TrashRestorePreview.self, from: Data(#"""
        {"entryId":"tr_20261118T170000Z_a1b2","reason":"delete-game","label":"Sky Garden Quest","conflict":true,
         "items":[
          {"name":"Roms/Game Boy Advance (GBA)/Sky Garden Quest.gba","size":16777216,"conflict":true,
           "freeName":"Roms/Game Boy Advance (GBA)/Sky Garden Quest (2).gba"},
          {"name":"Saves/GBA/Sky Garden Quest.gba.sav","size":131072,"conflict":false,
           "freeName":"Saves/GBA/Sky Garden Quest (2).gba.sav","future":1}],
         "restoredBytes":16908288}
        """#.utf8))
        #expect(preview.conflict && preview.items.count == 2 && preview.restoredBytes == 16_908_288)
        #expect(preview.items.map(\.conflict) == [true, false])
        #expect(preview.items[1].freeName == "Saves/GBA/Sky Garden Quest (2).gba.sav")

        let result = try ProtocolJSON.decoder().decode(TrashRestoreResult.self, from: Data(#"""
        {"restored":["Roms/Game Boy Advance (GBA)/Sky Garden Quest (2).gba"],"restoredBytes":16908288,"renamed":true}
        """#.utf8))
        #expect(result.renamed && result.restored.count == 1)
    }

    @Test func encodesTheRequestAndReadsTheConflicts() throws {
        let body = String(decoding: try JSONEncoder().encode(TrashRestoreRequest(onConflict: .rename)), as: UTF8.self)
        #expect(body == #"{"onConflict":"rename"}"#)
        let conflict = ServerError(status: 409, code: .conflict, message: "x",
                                   details: ["conflicts": .array([.string("Saves/GBA/A.gba.sav")])])
        #expect(conflict.trashRestoreConflicts == ["Saves/GBA/A.gba.sav"])
        #expect(ServerError(status: 409, code: .conflict, message: "x", details: ["existing": .string("a")]).trashRestoreConflicts == nil)
        #expect(ServerError(status: 404, code: .notFound, message: "x").trashRestoreConflicts == nil)
    }
}

@Suite("Restore from the trash against the fake pak (M18)")
struct TrashRestoreAPITests {
    let folder = "Handheld Advance (HA)"

    func restoreConflicts(_ error: Error) -> [String]? {
        guard case .server(let e) = error as? APIError, e.status == 409 else { return nil }
        return e.trashRestoreConflicts
    }

    @Test func aDeletedGameComesBackWithItsData() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let before = try await client.saveBackups(gameId: skyGarden).count
        let moved = try await client.deleteGame(id: skyGarden, keepSave: false)
        #expect(try await client.allGames().games.contains { $0.id == skyGarden } == false)

        let preview = try await client.trashRestorePreview(id: moved.trashEntryId)
        #expect(!preview.conflict && preview.reason == "delete-game" && preview.label == "Thistledown Isles")
        #expect(!preview.items.isEmpty)
        #expect(preview.items.allSatisfy { $0.freeName == $0.name && !$0.conflict })

        let result = try await client.restoreTrashEntry(id: moved.trashEntryId, onConflict: .fail)
        #expect(!result.renamed && result.restoredBytes == preview.restoredBytes)
        #expect(try await client.allGames().games.contains { $0.id == skyGarden })
        #expect(try await client.saveBackups(gameId: skyGarden).count == before)
        #expect(try await client.trash().entries.contains { $0.id == moved.trashEntryId } == false)
        // Gone from the trash: a second restore is a 404.
        await #expect { try await client.restoreTrashEntry(id: moved.trashEntryId, onConflict: .fail) } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 404
        }
    }

    @Test func aTakenPlaceIsAConflictThenAFreeName() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let moved = try await client.deleteGame(id: skyGarden, keepSave: false)
        pak.addGameFromAnotherClient(systemFolder: folder, path: "THISTLEDOWN ISLES.hab")

        let preview = try await client.trashRestorePreview(id: moved.trashEntryId)
        #expect(preview.conflict)
        let rom = try #require(preview.items.first { $0.name.hasSuffix(".hab") })
        #expect(rom.conflict && rom.freeName == "Roms/\(folder)/Thistledown Isles (2).hab")

        await #expect { try await client.restoreTrashEntry(id: moved.trashEntryId, onConflict: .fail) } throws: { error in
            restoreConflicts(error) == ["Roms/\(folder)/Thistledown Isles.hab"]
        }
        // Nothing changed: still in the trash, the present game untouched.
        #expect(try await client.trash().entries.contains { $0.id == moved.trashEntryId })

        let result = try await client.restoreTrashEntry(id: moved.trashEntryId, onConflict: .rename)
        #expect(result.renamed && result.restored.contains("Roms/\(folder)/Thistledown Isles (2).hab"))
        let names = try await client.allGames().games.filter { $0.systemId == DemoLibrary.id(folder) }.map(\.summary.fileName)
        #expect(names.contains("THISTLEDOWN ISLES.hab") && names.contains("Thistledown Isles (2).hab"))
    }

    @Test func aDeletedPakComesBackAndAPresentOneIsNeverReplaced() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let clock = try #require(try await client.paks().first { $0.name == "Clock" })
        let moved = try await client.deletePak(id: clock.pakId)
        let restored = try await client.restoreTrashEntry(id: moved.trashEntryId, onConflict: .fail)
        #expect(!restored.renamed)
        #expect(try await client.paks().contains { $0.name == "Clock" })

        // Deleted again, then another Clock installed: a conflict, then "Clock (2)".
        let again = try await client.deletePak(id: try #require(try await client.paks().first { $0.name == "Clock" }).pakId)
        pak.installPakFromAnotherClient(name: "clock")
        await #expect { try await client.restoreTrashEntry(id: again.trashEntryId, onConflict: .fail) } throws: { error in
            restoreConflicts(error) != nil
        }
        _ = try await client.restoreTrashEntry(id: again.trashEntryId, onConflict: .rename)
        #expect(Set(pak.installedPakNames).isSuperset(of: ["clock", "Clock (2)"]))
    }

    @Test func anUnknownEntryIsNotFoundAndABusyConsoleChangesNothing() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        await #expect { try await client.trashRestorePreview(id: "tr_missing") } throws: { error in
            guard case .server(let e) = error as? APIError else { return false }
            return e.status == 404
        }
        let entry = try #require(try await client.trash().entries.first)
        pak.busyNextWrites(1)
        await #expect { try await client.restoreTrashEntry(id: entry.id, onConflict: .fail) } throws: { ($0 as? APIError)?.isDeviceBusy == true }
        #expect(try await client.trash().entries.contains { $0.id == entry.id })
        // The seeded entry of the demo puts a game back.
        _ = try await client.restoreTrashEntry(id: entry.id, onConflict: .fail)
        #expect(try await client.allGames().games.contains { $0.summary.name == "Old Prototype" })
    }
}
