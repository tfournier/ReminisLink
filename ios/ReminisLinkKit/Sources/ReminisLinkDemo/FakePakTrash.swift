import Foundation
import ReminisLinkKit

// Milestone M18 of the fake pak: the restore from the trash
// (`GET` and `POST /trash/{entryId}/restore`). A deleted game comes back
// to its system with its data, a deleted or replaced pak to the paks; an
// entry with nothing to put back in the fake (a state, a backup copy)
// only leaves the trash. A place is taken when a game of the same file
// name (ignoring case) is in the folder, or a pak of the same name:
// `409 conflict` with `details.conflicts`, or every file under the free
// name " (N)" with `onConflict: rename`. The `library` event follows any
// restore (the `trash` path), and a `paks` event a restored pak.

/// What a trash entry of the fake pak puts back.
enum FakeTrashPayload {
    /// A deleted game; `data` nil when its save and states stayed in place
    /// (`keepSave`), or for a game never read (made from its counters).
    case game(systemFolder: String, game: DemoLibrary.Game, data: FakeGameData?)
    case pak(FakeInstalledPak)
}

extension FakePak {
    /// The plan of a restore (lock held): the preview items, the taken
    /// places and the first free number.
    private struct FakeRestorePlan {
        var entry: [String: Any]
        var payload: FakeTrashPayload?
        var stem: String
        var conflicts: [String]
        var free: Int?
        var bytes: Int64

        func freeName(_ path: String) -> String {
            guard let free, !stem.isEmpty, let range = path.range(of: "/" + stem + ".") else { return path }
            return path.replacingCharacters(in: range, with: "/\(stem) (\(free)).")
        }

        var items: [[String: Any]] {
            let files = entry["files"] as? [String] ?? []
            let share = files.isEmpty ? 0 : bytes / Int64(files.count)
            return files.enumerated().map { index, name in
                let size = index == 0 ? bytes - share * Int64(files.count - 1) : share
                let taken = conflicts.contains { name == $0 || name.hasPrefix($0 + "/") }
                return ["name": name, "size": size, "conflict": taken, "freeName": conflicts.isEmpty ? name : freeName(name)]
            }
        }
    }

    /// Whether a game file name or a pak name is taken (lock held).
    private func taken(_ payload: FakeTrashPayload, number: Int?) -> Bool {
        let suffix = number.map { " (\($0))" } ?? ""
        switch payload {
        case .game(let folder, let game, _):
            guard let system = _library.systems.first(where: { $0.folder == folder }) else { return false }
            let ext = game.fileName.dropFirst(game.name.count)
            let name = (game.name + suffix + ext).lowercased()
            return system.games.contains { $0.parent == game.parent && $0.fileName.lowercased() == name }
        case .pak(let pak):
            let name = (pak.name + suffix).lowercased()
            return paksState.paks.contains { $0.name.lowercased() == name }
        }
    }

    private func restorePlan(id: String) -> FakeRestorePlan? {
        guard let entry = trashEntries.first(where: { $0["id"] as? String == id }) else { return nil }
        let payload = trashPayloads[id]
        var plan = FakeRestorePlan(entry: entry, payload: payload, stem: "", conflicts: [], free: nil, bytes: Self.size(entry["sizeBytes"]))
        guard let payload else { return plan }
        switch payload {
        case .game(let folder, let game, _):
            plan.stem = game.name
            if taken(payload, number: nil) { plan.conflicts = ["Roms/\(folder)/\(game.path)"] }
        case .pak(let pak):
            plan.stem = pak.name
            if taken(payload, number: nil) { plan.conflicts = [pak.folder] }
        }
        if !plan.conflicts.isEmpty {
            plan.free = (2...999).first { !taken(payload, number: $0) }
        }
        return plan
    }

    func trashRestorePreview(id: String) -> FakeResponse {
        guard let plan = lock.withLock({ restorePlan(id: id) }) else {
            return .error(404, code: "not_found", message: "Trash entry not found")
        }
        return .json(200, [
            "entryId": id, "reason": plan.entry["reason"] as? String ?? "", "label": plan.entry["label"] as? String ?? "",
            "conflict": !plan.conflicts.isEmpty, "items": plan.items, "restoredBytes": plan.bytes,
        ])
    }

    func restoreTrashEntry(id: String, body: Data) -> FakeResponse {
        var rule = "fail"
        if !body.isEmpty {
            guard let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] else {
                return .error(400, code: "invalid_request", message: "Invalid JSON")
            }
            if let value = object["onConflict"] {
                guard let value = value as? String, value == "fail" || value == "rename" else {
                    return .error(400, code: "invalid_request", message: "onConflict must be fail or rename")
                }
                rule = value
            }
        }
        var pakRestored = false
        let response = lock.withLock { () -> FakeResponse in
            guard let plan = restorePlan(id: id) else {
                return .error(404, code: "not_found", message: "Trash entry not found")
            }
            let renamed = !plan.conflicts.isEmpty
            if renamed && (rule == "fail" || plan.free == nil) {
                return .error(409, code: "conflict", message: "A file exists where the entry goes back",
                              details: ["conflicts": plan.conflicts])
            }
            let suffix = renamed ? " (\(plan.free!))" : ""
            switch plan.payload {
            case .game(let folder, let game, let data)?:
                if let index = _library.systems.firstIndex(where: { $0.folder == folder }) {
                    let ext = game.fileName.dropFirst(game.name.count)
                    let fileName = game.name + suffix + ext
                    let path = game.parent.isEmpty ? fileName : game.parent + "/" + fileName
                    let back = DemoLibrary.Game(
                        path: path, kind: game.kind, sizeBytes: game.sizeBytes, modifiedAt: game.modifiedAt,
                        hasSave: game.hasSave, stateSlots: game.stateSlots, lockedStateCount: game.lockedStateCount,
                        cheatCount: game.cheatCount, hasArt: game.hasArt, hasClock: game.hasClock
                    )
                    _library.systems[index].games.append(back)
                    if let data {
                        gameData[Self.dataKey(system: _library.systems[index], game: back)] = data
                    }
                }
            case .pak(var pak)?:
                pak.name += suffix
                paksState.paks.append(pak)
                pakRestored = true
            case nil:
                break
            }
            trashEntries.removeAll { $0["id"] as? String == id }
            trashPayloads[id] = nil
            let restored = plan.items.compactMap { item -> String? in
                guard let name = item["name"] as? String else { return nil }
                return renamed ? plan.freeName(name) : name
            }
            return .json(200, ["restored": restored, "restoredBytes": plan.bytes, "renamed": renamed])
        }
        if pakRestored { emitPaks() }
        return response
    }
}

extension FakePak {
    /// As if another client had sent a game (tests: a place taken before a
    /// restore). `path` is relative to the system folder.
    public func addGameFromAnotherClient(systemFolder: String, path: String, sizeBytes: Int64 = 1024) {
        lock.withLock {
            guard let index = _library.systems.firstIndex(where: { $0.folder == systemFolder }) else { return }
            _library.systems[index].games.append(DemoLibrary.Game(path: path, sizeBytes: sizeBytes, modifiedAt: Date()))
        }
    }
}
