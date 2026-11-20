import Foundation
import ReminisLinkKit
import Testing
@testable import ReminisLink

/// Answers the reads of a ``CheatsLoader`` when the test says so, in any
/// order.
@MainActor
final class HeldReads {
    private var waiting: [CheckedContinuation<CheatList, Error>] = []

    var count: Int { waiting.count }

    func fetch() async throws -> CheatList {
        try await withCheckedThrowingContinuation { waiting.append($0) }
    }

    /// Answers the `index`-th read still waiting (0: the oldest).
    func answer(_ index: Int, with list: CheatList) {
        waiting.remove(at: index).resume(returning: list)
    }
}

/// Milestone M14 (review): every read of the Cheats screen goes through
/// one load generation, checked before anything is shown.
@MainActor
@Suite("Cheats loader (M14)")
struct CheatsLoaderTests {
    static func list(_ names: [String], etag: String) -> CheatList {
        CheatList(cheats: names.enumerated().map { Cheat(index: $0, description: $1, code: "\($0)", enabled: false, extra: [:]) },
                  etag: etag)
    }

    func waitFor(_ reads: HeldReads, count: Int) async {
        for _ in 0..<400 where reads.count < count { try? await Task.sleep(for: .milliseconds(5)) }
    }

    @Test func aChangeDuringTheFirstLoadStartsANewerOne() async throws {
        let loader = CheatsLoader()
        let reads = HeldReads()
        let first = Task { try await loader.load(reads.fetch) }
        await waitFor(reads, count: 1)
        // The console reports a change before the first answer.
        let refresh = Task { try await loader.refresh(canApply: { true }, reads.fetch) }
        await waitFor(reads, count: 2)
        reads.answer(1, with: Self.list(["New"], etag: "e2"))
        #expect(try await refresh.value == .applied)
        // The first answer, older, comes last: never shown.
        reads.answer(0, with: Self.list(["Old"], etag: "e1"))
        #expect(try await first.value == false)
        #expect(loader.editor?.cheats.map(\.description) == ["New"])
        #expect(loader.editor?.etag == "e2")
    }

    @Test func pullToRefreshKeepsTheSameListAndWaitsForASheet() async throws {
        let loader = CheatsLoader()
        let reads = HeldReads()
        let load = Task { try await loader.load(reads.fetch) }
        await waitFor(reads, count: 1)
        reads.answer(0, with: Self.list(["A", "B"], etag: "e1"))
        _ = try await load.value
        let ids = loader.editor?.cheats.map(\.id)
        // The same etag: the ids stay (an open sheet's Done still works).
        let same = Task { try await loader.refresh(canApply: { true }, reads.fetch) }
        await waitFor(reads, count: 1)
        reads.answer(0, with: Self.list(["A", "B"], etag: "e1"))
        #expect(try await same.value == .unchanged)
        #expect(loader.editor?.cheats.map(\.id) == ids)
        // Changed while a sheet is open: held back.
        let changed = Task { try await loader.refresh(canApply: { false }, reads.fetch) }
        await waitFor(reads, count: 1)
        reads.answer(0, with: Self.list(["C"], etag: "e2"))
        #expect(try await changed.value == .deferred)
        #expect(loader.editor?.cheats.map(\.id) == ids)
        #expect(loader.editor?.applyConsoleList() == true)
    }

    @Test func anOlderRefreshNeverOverwritesANewerOne() async throws {
        let loader = CheatsLoader()
        let reads = HeldReads()
        let load = Task { try await loader.load(reads.fetch) }
        await waitFor(reads, count: 1)
        reads.answer(0, with: Self.list(["A"], etag: "e1"))
        _ = try await load.value
        let older = Task { try await loader.refresh(canApply: { true }, reads.fetch) }
        await waitFor(reads, count: 1)
        let newer = Task { try await loader.refresh(canApply: { true }, reads.fetch) }
        await waitFor(reads, count: 2)
        reads.answer(1, with: Self.list(["C"], etag: "e3"))
        #expect(try await newer.value == .applied)
        reads.answer(0, with: Self.list(["B"], etag: "e2"))
        #expect(try await older.value == nil)
        #expect(loader.editor?.etag == "e3")
    }

    @Test func aRefreshDuringASaveWaitsForItsEnd() async throws {
        let loader = CheatsLoader()
        let reads = HeldReads()
        let load = Task { try await loader.load(reads.fetch) }
        await waitFor(reads, count: 1)
        reads.answer(0, with: Self.list(["A"], etag: "e1"))
        _ = try await load.value
        let save = Task { try await loader.save(reads.fetch) }
        await waitFor(reads, count: 1)
        #expect(try await loader.refresh(canApply: { true }, reads.fetch) == nil)
        reads.answer(0, with: Self.list(["A", "B"], etag: "e2"))
        try await save.value
        #expect(loader.editor?.etag == "e2")
        #expect(loader.takeMissedRefresh())
        #expect(!loader.takeMissedRefresh())
    }
}
