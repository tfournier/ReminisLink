import Foundation

/// Local edits of a cheat list, saved as a whole with `If-Match`
/// (docs/specs/04-api-v1.md, Cheats).
///
/// The editor keeps the list and the `etag` read from the pak. Edits stay
/// local until ``update()`` is sent. If the file changed on the console in
/// the meantime, the pak answers `412` (``isConflict(_:)``): the edits are
/// never sent again with another etag; the only way forward is to reload
/// (``load(_:)``), which discards them. Unknown keys (`extra`) travel back
/// untouched.
public struct CheatEditor: Sendable, Equatable {
    /// The list as last read from (or written to) the pak.
    public private(set) var original: [Cheat]
    /// The etag of ``original``, sent in `If-Match`.
    public private(set) var etag: String
    /// The list being edited.
    public private(set) var cheats: [Cheat]
    /// A newer list of the console not shown yet (M14): local edits, or a
    /// sheet editing a cheat, held it back. ``discardChanges()`` and
    /// ``applyConsoleList()`` show it.
    public private(set) var consoleList: CheatList?

    /// What a list read again after a change on the console did.
    public enum ConsoleChange: Sendable, Equatable {
        /// The same list (same etag, e.g. this iPhone's own save): nothing
        /// changes, the ids of the cheats stay (an open sheet still works).
        case unchanged
        /// Shown.
        case applied
        /// Kept for later: a sheet edits a cheat of the shown list.
        case deferred
        /// Kept for later: local edits are never clobbered, and they can no
        /// longer be saved over the console's list (`412`).
        case conflict
    }

    public init(_ list: CheatList) {
        original = list.cheats
        etag = list.etag
        cheats = list.cheats
    }

    /// Edits not saved yet (order, content, additions, deletions).
    public var hasChanges: Bool { cheats != original }

    /// Replaces everything with a list read from the pak (after a save, an
    /// import or a reload): local edits are dropped.
    public mutating func load(_ list: CheatList) {
        original = list.cheats
        etag = list.etag
        cheats = list.cheats
        consoleList = nil
    }

    /// The list read again after the console reported a change (M14,
    /// `library` event). `canApply`: no sheet is editing a cheat of the
    /// shown list.
    public mutating func consoleChanged(_ list: CheatList, canApply: Bool) -> ConsoleChange {
        guard list.etag != etag else {
            consoleList = nil
            return .unchanged
        }
        guard !hasChanges else {
            consoleList = list
            return .conflict
        }
        guard canApply else {
            consoleList = list
            return .deferred
        }
        load(list)
        return .applied
    }

    /// Shows the list held back by a sheet, once it closed (never over
    /// local edits). Returns whether it did.
    @discardableResult
    public mutating func applyConsoleList() -> Bool {
        guard !hasChanges, let list = consoleList else { return false }
        load(list)
        return true
    }

    /// Drops the local edits; a newer list of the console, if one was
    /// held back, is shown.
    public mutating func discardChanges() {
        if let list = consoleList {
            load(list)
        } else {
            cheats = original
        }
    }

    /// Adds a cheat at the end. Returns false when it is not valid.
    @discardableResult
    public mutating func add(description: String, code: String, enabled: Bool) -> Bool {
        guard let (d, c) = Self.validated(description: description, code: code) else { return false }
        cheats.append(Cheat(description: d, code: c, enabled: enabled))
        return true
    }

    /// Changes a cheat (its `extra` keys are kept). Returns false when it
    /// is not valid or no longer in the list.
    @discardableResult
    public mutating func update(id: UUID, description: String, code: String, enabled: Bool) -> Bool {
        guard let index = cheats.firstIndex(where: { $0.id == id }),
              let (d, c) = Self.validated(description: description, code: code)
        else { return false }
        cheats[index].description = d
        cheats[index].code = c
        cheats[index].enabled = enabled
        return true
    }

    public mutating func setEnabled(id: UUID, _ enabled: Bool) {
        guard let index = cheats.firstIndex(where: { $0.id == id }) else { return }
        cheats[index].enabled = enabled
    }

    public mutating func remove(atOffsets offsets: IndexSet) {
        for index in offsets.sorted(by: >) where cheats.indices.contains(index) {
            cheats.remove(at: index)
        }
    }

    public mutating func remove(id: UUID) {
        cheats.removeAll { $0.id == id }
    }

    /// Moves cheats like `List.onMove`: `destination` is an index in the
    /// list before the move.
    public mutating func move(fromOffsets source: IndexSet, toOffset destination: Int) {
        let valid = source.filter { cheats.indices.contains($0) }
        guard !valid.isEmpty else { return }
        let moving = valid.map { cheats[$0] }
        let before = valid.filter { $0 < destination }.count
        for index in valid.sorted(by: >) { cheats.remove(at: index) }
        let target = min(max(destination - before, 0), cheats.count)
        cheats.insert(contentsOf: moving, at: target)
    }

    /// The body of `PUT /cheats`: the whole list in order, renumbered.
    public func update() -> CheatsUpdate {
        CheatsUpdate(cheats: cheats.enumerated().map { offset, cheat in
            var cheat = cheat
            cheat.index = offset
            return cheat
        })
    }

    /// Whether a failed save means the file changed on the console
    /// (`412 Precondition Failed`, code `conflict`).
    public static func isConflict(_ error: Error) -> Bool {
        guard case APIError.server(let server) = error else { return false }
        return server.status == 412
    }

    /// Trimmed description and code, both required and on one line (a line
    /// break would end the value in the `.cht` file).
    public static func validated(description: String, code: String) -> (String, String)? {
        let d = singleLine(description)
        let c = singleLine(code)
        guard !d.isEmpty, !c.isEmpty else { return nil }
        return (d, c)
    }

    static func singleLine(_ text: String) -> String {
        text.components(separatedBy: .newlines).joined(separator: " ")
            .trimmingCharacters(in: .whitespaces)
    }
}
