import Foundation
import ReminisLinkKit

// Milestone M18 of the fake pak: the restore from the trash
// (`GET` and `POST /trash/{entryId}/restore`). What an entry puts back is
// its payload: a deleted game comes back to its system with its data, a
// deleted or replaced pak to the paks, a deleted backup copy to the
// backups of its game, a deleted state to its slot. A place is taken when
// a game of the same file name (ignoring case), a pak of the same name, a
// backup of the same id or a state in the slot is there: `409 conflict`
// with `details.conflicts`, or every file under the free name " (N)" with
// `onConflict: rename` (the files of a state or backup then go to a name
// the app does not show, as on the real pak). A deleted backup copy of a
// game that still exists can replace its current save (`replace`, the
// current save kept as a `before-restore` copy, as internal/transfer
// does); anything else answers `422`. An entry without a payload (made by
// a test, or files the fake does not model: a locked copy, a replaced
// ROM) has its place taken, as these entries replaced a file that is
// still there: `409`. The fake never offers nor performs an action it
// cannot show: no free name for a state, a backup copy or an entry it
// does not model (their free name is a file the app never lists), so
// `freeNameUnavailable` says none exists and `rename` answers `409`. The `library` event
// follows any restore (the `trash` path), and a `paks` event a restored
// pak.

/// What a trash entry of the fake pak puts back.
enum FakeTrashPayload {
    /// A deleted game and its data as they were (nil for a game never
    /// read: made from its counters); `keptSave`: deleted with keepSave,
    /// only its cheats went to the trash and come back.
    case game(systemFolder: String, game: DemoLibrary.Game, data: FakeGameData?, keptSave: Bool)
    case pak(FakeInstalledPak)
    /// A backup copy of the save of the game whose data key is `key`.
    case backup(key: String, backup: FakeBackup)
    /// A deleted state of a slot of the game whose data key is `key`.
    case state(key: String, slot: Int, entry: FakeSlot)
    /// Screenshots moved to the trash (M22): back in the screenshots
    /// folder, " (N)" before the extension with `rename`.
    case screenshots([FakeScreenshot])
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
        /// Where the current save goes with `replace` (nil: not replaceable).
        var replace: (target: String, displaced: [String])?
        /// Why `replace` is not possible (nil when it is).
        var replaceUnavailable: String?

        func freeName(_ path: String) -> String {
            guard let free else { return path }
            if !stem.isEmpty, let range = path.range(of: "/" + stem + ".", options: .caseInsensitive) {
                return path.replacingCharacters(in: range, with: "/\(path[range].dropFirst().dropLast()) (\(free)).")
            }
            // No game name in the path: before the extension.
            let ext = (path as NSString).pathExtension
            let base = ext.isEmpty ? path : String(path.dropLast(ext.count + 1))
            return ext.isEmpty ? "\(base) (\(free))" : "\(base) (\(free)).\(ext)"
        }

        var items: [[String: Any]] {
            let files = entry["files"] as? [String] ?? []
            let share = files.isEmpty ? 0 : bytes / Int64(files.count)
            return files.enumerated().map { index, name in
                let size = index == 0 ? bytes - share * Int64(files.count - 1) : share
                let taken = conflicts.contains { name == $0 || name.hasPrefix($0 + "/") }
                let free: Any = conflicts.isEmpty ? name : (self.free.map { _ in freeName(name) as Any } ?? NSNull())
                return ["name": name, "size": size, "conflict": taken, "freeName": free,
                        "replaceName": replace.map { $0.target as Any } ?? NSNull()]
            }
        }
    }

    /// Whether the place of a payload is taken, with a free number or
    /// without (lock held).
    private func taken(_ payload: FakeTrashPayload, number: Int?) -> Bool {
        let suffix = number.map { " (\($0))" } ?? ""
        switch payload {
        case .game(let folder, let game, _, _):
            guard let system = _library.systems.first(where: { $0.folder == folder }) else { return false }
            let ext = game.fileName.dropFirst(game.name.count)
            let name = (game.name + suffix + ext).lowercased()
            return system.games.contains { $0.parent == game.parent && $0.fileName.lowercased() == name }
        case .pak(let pak):
            let name = (pak.name + suffix).lowercased()
            return paksState.paks.contains { $0.name.lowercased() == name }
        case .backup(let key, let backup):
            // A free name is a file the backups do not list: never taken.
            return number == nil && gameData[key]?.backups.contains { $0.id == backup.id } == true
        case .state(let key, let slot, _):
            return number == nil && gameData[key]?.slots[slot]?.state != nil
        case .screenshots(let shots):
            return shots.contains { screenshotTaken(Self.screenshotName($0.name, number: number)) }
        }
    }

    /// The game and system of a data key (lock held).
    private func gameOf(key: String) -> (DemoLibrary.System, DemoLibrary.Game)? {
        for system in _library.systems {
            for game in system.games where Self.dataKey(system: system, game: game) == key {
                return (system, game)
            }
        }
        return nil
    }

    private func restorePlan(id: String) -> FakeRestorePlan? {
        guard let entry = trashEntries.first(where: { $0["id"] as? String == id }) else { return nil }
        let payload = trashPayloads[id]
        let files = entry["files"] as? [String] ?? []
        var plan = FakeRestorePlan(entry: entry, payload: payload, stem: "", conflicts: [], free: nil, bytes: Self.size(entry["sizeBytes"]))
        guard let payload else {
            // Not modelled: the file it replaced is still there, and no
            // free name the app could show.
            plan.conflicts = files.isEmpty ? [entry["label"] as? String ?? id] : files
            plan.replaceUnavailable = "not-a-save"
            return plan
        }
        switch payload {
        case .game(let folder, let game, _, _):
            plan.stem = game.name
            if taken(payload, number: nil) { plan.conflicts = ["Roms/\(folder)/\(game.path)"] }
        case .pak(let pak):
            plan.stem = pak.name
            if taken(payload, number: nil) { plan.conflicts = [pak.folder] }
        case .backup(let key, _):
            if taken(payload, number: nil) { plan.conflicts = files }
            if let (system, game) = gameOf(key: key) {
                if plan.conflicts.isEmpty {
                    let save = Self.savePath(system, game)
                    let hasSave = gameData[key].map { $0.save != nil } ?? game.hasSave
                    plan.replace = (save, hasSave ? [save] : [])
                } else {
                    plan.replaceUnavailable = "backup-taken"
                }
            } else {
                plan.replaceUnavailable = "game-missing"
            }
            // A free name would be a file the backups never list.
            return plan
        case .state(_, _, _):
            if taken(payload, number: nil) { plan.conflicts = files }
            plan.replaceUnavailable = "not-a-save"
            return plan
        case .screenshots(let shots):
            plan.conflicts = shots.filter { screenshotTaken($0.name) }.map { "Screenshots/" + $0.name }
        }
        plan.replaceUnavailable = "not-a-save"
        if !plan.conflicts.isEmpty {
            plan.free = (2...999).first { !taken(payload, number: $0) }
        }
        return plan
    }

    func trashRestorePreview(id: String) -> FakeResponse {
        guard let plan = lock.withLock({ restorePlan(id: id) }) else {
            return .error(404, code: "not_found", message: "Trash entry not found")
        }
        let unavailable: Any = !plan.conflicts.isEmpty && plan.free == nil ? "all-taken" : NSNull()
        return .json(200, [
            "entryId": id, "reason": plan.entry["reason"] as? String ?? "", "label": plan.entry["label"] as? String ?? "",
            "conflict": !plan.conflicts.isEmpty, "items": plan.items, "restoredBytes": plan.bytes,
            "freeNameUnavailable": unavailable, "replaceable": plan.replace != nil,
            "replaceDisplaced": plan.replace?.displaced ?? [],
            "replaceKeptAs": plan.replace.map { $0.displaced.isEmpty ? NSNull() : "backup" as Any } ?? NSNull(),
            "replaceUnavailable": plan.replaceUnavailable.map { $0 as Any } ?? NSNull(),
        ])
    }

    func restoreTrashEntry(id: String, body: Data) -> FakeResponse {
        var rule = "fail"
        let trimmed = String(decoding: body, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        if !trimmed.isEmpty {
            guard trimmed.hasPrefix("{"), let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any] else {
                return .error(400, code: "invalid_request", message: "The body must be a JSON object")
            }
            if let value = object["onConflict"] {
                guard let value = value as? String, ["fail", "rename", "replace"].contains(value) else {
                    return .error(400, code: "invalid_request", message: "onConflict must be fail, rename or replace")
                }
                rule = value
            }
        }
        var pakRestored = false
        var screenshotsRestored = false
        let response = lock.withLock { () -> FakeResponse in
            guard let plan = restorePlan(id: id) else {
                return .error(404, code: "not_found", message: "Trash entry not found")
            }
            if rule == "replace" {
                return replaceWithBackup(id: id, plan: plan)
            }
            let renamed = !plan.conflicts.isEmpty
            if renamed && (rule == "fail" || plan.free == nil) {
                var details: [String: Any] = ["conflicts": plan.conflicts]
                if rule == "rename" { details["freeNameUnavailable"] = "all-taken" }
                return .error(409, code: "conflict", message: "A file exists where the entry goes back", details: details)
            }
            let suffix = renamed ? " (\(plan.free!))" : ""
            switch plan.payload {
            case .game(let folder, let game, let data, let keptSave)?:
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
                    let key = Self.dataKey(system: _library.systems[index], game: back)
                    if let data, keptSave {
                        // The save and states stayed: only the cheats come back.
                        var current = gameData[key] ?? seed(game: back)
                        current.cheats = data.cheats
                        gameData[key] = current
                    } else if let data {
                        gameData[key] = data
                    }
                }
            case .pak(var pak)?:
                pak.name += suffix
                paksState.paks.append(pak)
                pakRestored = true
            case .backup(let key, let backup)?:
                gameData[key]?.backups.insert(backup, at: 0)
            case .state(let key, let slot, let entry)?:
                gameData[key]?.slots[slot] = entry
            case .screenshots(let shots)?:
                for var shot in shots {
                    shot.name = Self.screenshotName(shot.name, number: renamed ? plan.free : nil)
                    screenshotsState.screenshots.append(shot)
                }
                screenshotsRestored = true
            case nil:
                break // never reached: a conflict without a free name
            }
            trashEntries.removeAll { $0["id"] as? String == id }
            trashPayloads[id] = nil
            let restored = plan.items.compactMap { item -> String? in
                guard let name = item["name"] as? String else { return nil }
                return renamed ? plan.freeName(name) : name
            }
            return .json(200, ["restored": restored, "restoredBytes": plan.bytes, "renamed": renamed,
                               "replacedEntryId": NSNull(), "replaced": [String](), "replacedKeptAs": NSNull()])
        }
        if pakRestored { emitPaks() }
        if screenshotsRestored { emitScreenshots() }
        return response
    }

    /// `replace` (lock held): only a deleted backup copy of a game that
    /// still exists; the current save is kept as a `before-restore` copy.
    private func replaceWithBackup(id: String, plan: FakeRestorePlan) -> FakeResponse {
        guard case .backup(let key, let backup)? = plan.payload, let replace = plan.replace,
              let (system, game) = gameOf(key: key) else {
            return .error(422, code: "unsupported_format", message: "Only the save of an existing game can replace the current one",
                          details: ["reason": plan.replaceUnavailable ?? "not-a-save"])
        }
        var data = gameData[key] ?? seed(game: game)
        if let current = data.save {
            let now = Date()
            data.backups.insert(FakeBackup(id: "bk_\(Self.stamp(now))_\(UUID().uuidString.prefix(4).lowercased())",
                                           label: nil, createdAt: now, blob: current, reason: "before-restore"), at: 0)
        }
        data.backups.insert(backup, at: 0)
        data.save = backup.blob
        data.saveModifiedAt = Date()
        gameData[key] = data
        trashEntries.removeAll { $0["id"] as? String == id }
        trashPayloads[id] = nil
        return .json(200, ["restored": [Self.savePath(system, game)], "restoredBytes": plan.bytes, "renamed": false,
                           "replacedEntryId": NSNull(), "replaced": replace.displaced,
                           "replacedKeptAs": replace.displaced.isEmpty ? NSNull() : "backup" as Any])
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
