import Foundation
import Observation
import ReminisLinkKit

/// The cheat list of the Cheats screen and every read of it: the first
/// load, an explicit reload, pull-to-refresh, a change reported by the
/// console (M14) and the answer of a save all go through one
/// ``LoadGeneration``, checked before anything is shown. Only the last
/// one started publishes, whatever order the answers come back in.
@MainActor
@Observable
final class CheatsLoader {
    typealias Fetch = () async throws -> CheatList

    var editor: CheatEditor?
    @ObservationIgnored private var reads = LoadGeneration()
    @ObservationIgnored private var isSaving = false
    /// A refresh asked while a save was running: done once it ends.
    @ObservationIgnored private(set) var refreshMissedWhileSaving = false

    /// Shows the console's list, replacing the editor (the first load, the
    /// explicit Reload after a conflict, the end of an import). Returns
    /// false when a newer read made this one stale (nothing shown, its
    /// error not thrown).
    @discardableResult
    func load(_ fetch: Fetch) async throws -> Bool {
        let generation = reads.begin()
        let list: CheatList
        do {
            list = try await fetch()
        } catch {
            guard reads.isCurrent(generation) else { return false }
            throw error
        }
        guard reads.isCurrent(generation) else { return false }
        if editor == nil {
            editor = CheatEditor(list)
        } else {
            editor?.load(list)
        }
        return true
    }

    /// Pull-to-refresh and changes on the console: never over local edits
    /// or an open sheet (``CheatEditor/consoleChanged(_:canApply:)``).
    /// Before the first answer, it is a newer first load (the one in
    /// flight may be older than the change). `canApply` is asked when the
    /// answer arrives. Nil when stale, or put off by a running save.
    func refresh(canApply: () -> Bool, _ fetch: Fetch) async throws -> CheatEditor.ConsoleChange? {
        guard !isSaving else {
            refreshMissedWhileSaving = true
            return nil
        }
        guard editor != nil else {
            return try await load(fetch) ? .applied : nil
        }
        let generation = reads.begin()
        let list: CheatList
        do {
            list = try await fetch()
        } catch {
            guard reads.isCurrent(generation) else { return nil }
            throw error
        }
        guard reads.isCurrent(generation) else { return nil }
        return editor?.consoleChanged(list, canApply: canApply())
    }

    /// Sends the edits; the saved list is shown unless a newer read was
    /// started since (none can start while it runs: they wait for its end,
    /// see ``refreshMissedWhileSaving``).
    func save(_ send: Fetch) async throws {
        isSaving = true
        defer { isSaving = false }
        let generation = reads.begin()
        let saved = try await send()
        guard reads.isCurrent(generation) else { return }
        editor?.load(saved)
    }

    /// Whether a refresh was put off by the last save (and clears it).
    func takeMissedRefresh() -> Bool {
        defer { refreshMissedWhileSaving = false }
        return refreshMissedWhileSaving
    }
}
