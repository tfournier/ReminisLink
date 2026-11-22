import Foundation
import ReminisLinkKit

// Milestone M23 of the fake pak: the writes of the SD browser (the
// `sd-file` upload target, `POST /sd/folders`, `POST /sd/trash`) and the
// renaming of a game (`POST /games/{gameId}/rename`). It follows
// protocol/openapi.yaml: the protected places of NextUI (hidden entries
// at the root, the main folders, the own pak), never an overwrite
// (`409 conflict` with `details.existing` and `details.freeName`), entries
// to the trash in one entry (reason `delete-file`) restored by the M18
// restore. A file added in a system folder becomes a game; a pak folder
// moved to the trash leaves the paks. A rename keeps the data of the game
// (its save, states and cheats follow its new name).

/// A file or folder added through the SD browser, outside the library.
struct FakeSdExtra {
    /// Nil for a folder.
    let size: Int64?
    let modifiedAt: Date
}

/// What a `delete-file` trash entry of the fake puts back.
struct FakeSdTrash {
    /// The paths moved (top level of the request).
    var paths: [String]
    var games: [(systemFolder: String, game: DemoLibrary.Game)] = []
    var paks: [FakeInstalledPak] = []
    var extras: [String: FakeSdExtra] = [:]
    /// Paths of the made-up card hidden by the move (lowercased).
    var removed: [String] = []
}

/// A refusal of the SD browser.
struct FakeSdRefusal: Error {
    let response: FakeResponse
}

extension FakePak {
    /// The main folders of the fake NextUI card (never moved to the trash).
    static let sdRoots: Set<String> = ["roms", "saves", "bios", "tools", "emus", "tools/tg5040", "emus/tg5040", "screenshots", "cheats"]

    /// The folder of the running pak (ReminisLink's).
    static let sdOwnPak = "tools/tg5040/reminislink.pak"

    /// Why a place (and what is inside it) is read only, nil when it is
    /// writable.
    static func sdReadOnly(_ path: String) -> String? {
        let lower = path.lowercased()
        if lower == sdOwnPak || lower.hasPrefix(sdOwnPak + "/") { return "data" }
        if lower.hasPrefix(".") { return "system" }
        return nil
    }

    /// Why an entry cannot go to the trash, nil when it can.
    static func sdDeleteProtection(_ path: String) -> String? {
        if path.isEmpty { return "root" }
        if let why = sdReadOnly(path) { return why }
        let lower = path.lowercased()
        if sdRoots.contains(lower) { return "root" }
        if sdOwnPak.hasPrefix(lower + "/") { return "data" }
        if sdRoots.contains(where: { $0.hasPrefix(lower + "/") }) { return "root" }
        return nil
    }

    /// The extras and removals on top of the made-up card.
    func applySdChanges(to root: inout FakeSdNode) {
        let (extras, removed) = lock.withLock { (paksState.sdExtras, paksState.sdRemoved) }
        func remove(_ node: inout FakeSdNode, prefix: String) {
            node.children.removeAll { child in
                let path = prefix.isEmpty ? child.name : prefix + "/" + child.name
                return removed.contains(path.lowercased())
            }
            for i in node.children.indices where node.children[i].isFolder {
                let name = node.children[i].name
                remove(&node.children[i], prefix: prefix.isEmpty ? name : prefix + "/" + name)
            }
        }
        remove(&root, prefix: "")
        for (path, extra) in extras.sorted(by: { $0.key < $1.key }) {
            insert(&root, parts: path.split(separator: "/").map(String.init), extra: extra)
        }
    }

    private func insert(_ node: inout FakeSdNode, parts: [String], extra: FakeSdExtra) {
        guard let first = parts.first else { return }
        if parts.count == 1 {
            node.children.removeAll { $0.name == first }
            node.children.append(FakeSdNode(name: first, size: extra.size, modifiedAt: extra.modifiedAt))
            return
        }
        if !node.children.contains(where: { $0.name == first && $0.isFolder }) {
            node.children.append(FakeSdNode(name: first, size: nil, modifiedAt: extra.modifiedAt))
        }
        if let i = node.children.firstIndex(where: { $0.name == first && $0.isFolder }) {
            insert(&node.children[i], parts: Array(parts.dropFirst()), extra: extra)
        }
    }

    /// The writable folder of a `folderId` (`NSNull`/nil: the root).
    func sdWritableFolder(_ raw: Any?) -> Result<String, FakeSdRefusal> {
        var path = ""
        if let id = raw as? String {
            guard let decoded = Self.sdPath(of: id) else {
                return .failure(FakeSdRefusal(response: .error(400, code: "invalid_id", message: "Invalid identifier")))
            }
            path = decoded
        } else if raw != nil && !(raw is NSNull) {
            return .failure(FakeSdRefusal(response: .error(400, code: "invalid_request", message: "Invalid folderId")))
        }
        guard let node = node(at: path), node.isFolder else {
            return .failure(FakeSdRefusal(response: .error(404, code: "not_found", message: "Folder not found")))
        }
        if let why = Self.sdReadOnly(path) {
            return .failure(FakeSdRefusal(response: .error(409, code: "conflict", message: "Protected place", details: ["protected": why])))
        }
        return .success(path)
    }

    private static func join(_ folder: String, _ name: String) -> String {
        folder.isEmpty ? name : folder + "/" + name
    }

    /// The free name " (N)" of `name` in `folder`, nil when none.
    private func sdFreeName(folder: String, name: String) -> String? {
        let names = Set((node(at: folder)?.children ?? []).map { $0.name.lowercased() })
        guard names.contains(name.lowercased()) else { return name }
        let ext = (name as NSString).pathExtension
        let base = ext.isEmpty ? name : String(name.dropLast(ext.count + 1))
        for n in 2...999 {
            let candidate = ext.isEmpty ? "\(base) (\(n))" : "\(base) (\(n)).\(ext)"
            if !names.contains(candidate.lowercased()) { return candidate }
        }
        return nil
    }

    /// `409 conflict` when `name` is taken in `folder` (with the free name).
    func sdConflict(folder: String, name: String) -> FakeResponse? {
        guard let existing = node(at: folder)?.children.first(where: { $0.name.lowercased() == name.lowercased() }) else {
            return nil
        }
        let free = sdFreeName(folder: folder, name: name)
        return .error(409, code: "conflict", message: "Target file exists", details: [
            "existing": Self.join(folder, existing.name), "freeName": free.map { $0 as Any } ?? NSNull(),
        ])
    }

    /// The system and the folder inside it of a path under Roms/, if any.
    private func systemPlace(of path: String) -> (DemoLibrary.System, String)? {
        let parts = path.split(separator: "/", maxSplits: 2).map(String.init)
        guard parts.count >= 2, parts[0] == "Roms", let system = library.systems.first(where: { $0.folder == parts[1] }) else {
            return nil
        }
        return (system, parts.count == 3 ? parts[2] : "")
    }

    /// Installs an uploaded file in a folder of the browser.
    func storeSdFile(folder: String, name: String, sizeBytes: Int64, onConflict: String) -> FakeResponse {
        var target = name
        if let conflict = sdConflict(folder: folder, name: name) {
            guard onConflict == "rename", let free = sdFreeName(folder: folder, name: name) else { return conflict }
            target = free
        }
        let path = Self.join(folder, target)
        if let (system, inside) = systemPlace(of: path) {
            // A game of the library once it lands in a system folder.
            let game = DemoLibrary.Game(path: inside, sizeBytes: sizeBytes, modifiedAt: Date())
            lock.withLock {
                if let index = _library.systems.firstIndex(where: { $0.id == system.id }) {
                    _library.systems[index].games.append(game)
                }
            }
        } else {
            lock.withLock {
                paksState.sdRemoved.remove(path.lowercased())
                paksState.sdExtras[path] = FakeSdExtra(size: sizeBytes, modifiedAt: Date())
            }
        }
        return .json(200, ["sdEntryId": Self.sdEntryId(path)])
    }

    /// `POST /sd/folders`.
    func createSdFolder(body: Data) -> FakeResponse {
        guard let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
              let rawName = object["name"] as? String
        else { return .error(400, code: "invalid_request", message: "Missing name") }
        guard let name = Self.cleanFileName(rawName), !name.hasPrefix(".") else {
            return .error(400, code: "invalid_name", message: "Invalid folder name")
        }
        let folder: String
        switch sdWritableFolder(object["parentId"]) {
        case .success(let f): folder = f
        case .failure(let refusal): return refusal.response
        }
        if let existing = node(at: folder)?.children.first(where: { $0.name.lowercased() == name.lowercased() }) {
            return .error(409, code: "conflict", message: "Target file exists", details: ["existing": Self.join(folder, existing.name)])
        }
        let path = Self.join(folder, name)
        let now = Date()
        lock.withLock {
            paksState.sdRemoved.remove(path.lowercased())
            paksState.sdExtras[path] = FakeSdExtra(size: nil, modifiedAt: now)
        }
        return .json(201, [
            "sdEntryId": Self.sdEntryId(path), "name": name, "kind": "folder", "sizeBytes": NSNull(),
            "modifiedAt": ProtocolJSON.formatDate(now), "deletable": true, "protection": NSNull(),
        ])
    }

    /// The regular files under a node (paths), and their size.
    private func files(of node: FakeSdNode, at path: String) -> ([String], Int64) {
        if let size = node.size { return ([path], size) }
        var out: [String] = []
        var bytes: Int64 = 0
        for child in node.children {
            let (f, b) = files(of: child, at: Self.join(path, child.name))
            out += f
            bytes += b
        }
        return (out, bytes)
    }

    /// `POST /sd/trash`: all or nothing, in one entry.
    func trashSdEntries(body: Data) -> FakeResponse {
        guard let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any],
              let ids = object["sdEntryIds"] as? [String], (1...SdTrashRequest.maxIds).contains(ids.count)
        else { return .error(400, code: "invalid_request", message: "sdEntryIds must hold 1 to 100 identifiers") }
        var seen = Set<String>()
        var paths: [String] = []
        var missing: [String] = []
        var refused: [String] = []
        var reason: String?
        for id in ids where seen.insert(id).inserted {
            guard let path = Self.sdPath(of: id) else { return .error(400, code: "invalid_id", message: "Invalid identifier") }
            guard node(at: path) != nil else {
                missing.append(id)
                continue
            }
            if let why = Self.sdDeleteProtection(path) {
                reason = reason ?? why
                refused.append(id)
                continue
            }
            paths.append(path)
        }
        if !missing.isEmpty {
            return .error(404, code: "not_found", message: "Entry not found", details: ["missing": missing])
        }
        if let reason {
            return .error(409, code: "conflict", message: "Protected place", details: ["protected": reason, "refused": refused])
        }
        // An entry inside another of the list moves with it.
        paths = paths.filter { p in !paths.contains { $0 != p && p.lowercased().hasPrefix($0.lowercased() + "/") } }
        var moved: [String] = []
        var bytes: Int64 = 0
        for path in paths {
            if let node = node(at: path) {
                let (f, b) = files(of: node, at: path)
                moved += f
                bytes += b
            }
        }
        var paksChanged = false
        let response = lock.withLock { () -> FakeResponse in
            var trash = FakeSdTrash(paths: paths)
            for path in paths {
                let lower = path.lowercased()
                let under = { (p: String) in p.lowercased() == lower || p.lowercased().hasPrefix(lower + "/") }
                // Games of the library.
                for (index, system) in _library.systems.enumerated() {
                    let prefix = "Roms/" + system.folder
                    let gone = system.games.filter { under(prefix + "/" + $0.path) || under(prefix) }
                    trash.games += gone.map { (system.folder, $0) }
                    _library.systems[index].games.removeAll { g in gone.contains { $0.path == g.path } }
                }
                // Paks.
                let paks = paksState.paks.filter { under(($0.kind == "tool" ? "Tools/tg5040/" : "Emus/tg5040/") + $0.name + ".pak") }
                trash.paks += paks
                if !paks.isEmpty { paksChanged = true }
                paksState.paks.removeAll { pak in paks.contains { $0.pakId == pak.pakId } }
                // Added files and folders, and the made-up card.
                for (p, extra) in paksState.sdExtras where under(p) {
                    trash.extras[p] = extra
                    paksState.sdExtras[p] = nil
                }
                trash.removed.append(lower)
                paksState.sdRemoved.insert(lower)
            }
            let now = Date()
            let stamp = ProtocolJSON.formatDate(now).replacingOccurrences(of: "-", with: "").replacingOccurrences(of: ":", with: "")
            let entryId = "tr_\(stamp)_\(UUID().uuidString.prefix(4).lowercased())"
            let label = paths.count == 1 ? String(paths[0].split(separator: "/").last ?? "") : "\(paths.count) items"
            trashEntries.insert([
                "id": entryId, "trashedAt": ProtocolJSON.formatDate(now), "reason": "delete-file", "label": label,
                "files": moved, "sizeBytes": bytes, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
            ], at: 0)
            trashPayloads[entryId] = .sdEntries(trash)
            return .json(200, ["trashEntryId": entryId, "moved": moved, "movedBytes": bytes])
        }
        if paksChanged { emitPaks() }
        return response
    }

    /// The places of a `delete-file` entry taken since (lock held: reads
    /// the state directly, never through ``node(at:)``).
    func sdTakenPlaces(_ trash: FakeSdTrash) -> [String] {
        trash.paths.filter { path in
            let lower = path.lowercased()
            if paksState.sdExtras.keys.contains(where: { $0.lowercased() == lower }) { return true }
            if trash.games.contains(where: { entry in
                _library.systems.first { $0.folder == entry.systemFolder }?.games
                    .contains { $0.path.lowercased() == entry.game.path.lowercased() } == true
            }) { return true }
            return trash.paks.contains { pak in paksState.paks.contains { $0.name.lowercased() == pak.name.lowercased() } }
        }
    }

    /// Puts a `delete-file` entry back (lock held); true when a pak came
    /// back.
    func restoreSdEntries(_ trash: FakeSdTrash) -> Bool {
        for path in trash.removed { paksState.sdRemoved.remove(path) }
        for (path, extra) in trash.extras { paksState.sdExtras[path] = extra }
        for entry in trash.games {
            if let index = _library.systems.firstIndex(where: { $0.folder == entry.systemFolder }) {
                _library.systems[index].games.append(entry.game)
            }
        }
        paksState.paks += trash.paks
        return !trash.paks.isEmpty
    }

    // MARK: - Rename a game

    func routeFiles(method: String, segments: [String], body: Data) -> FakeResponse? {
        if method == "POST", segments.count == 3, segments[0] == "games", segments[2] == "rename" {
            return renameGame(id: segments[1], body: body)
        }
        return nil
    }

    private func renameGame(id: String, body: Data) -> FakeResponse {
        guard let object = (try? JSONSerialization.jsonObject(with: body)) as? [String: Any], let raw = object["name"] as? String else {
            return .error(400, code: "invalid_request", message: "Missing name")
        }
        guard let (system, game) = library.game(id: id) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        let name = GameNameRule.normalized(raw)
        let ext = GameNameRule.keptExtension(fileName: game.fileName, isFolder: game.kind.isFolder)
        guard GameNameRule.problem(name, fileExtension: ext) == nil else {
            return .error(400, code: "invalid_name", message: "Invalid game name")
        }
        let fileName = name + ext
        let path = game.parent.isEmpty ? fileName : game.parent + "/" + fileName
        if path != game.path,
           system.games.contains(where: { $0.path != game.path && $0.fileName.lowercased() == fileName.lowercased() }) {
            return .error(409, code: "conflict", message: "Name already taken",
                          details: ["conflicts": ["Roms/\(system.folder)/\(path)"]])
        }
        var renamed: [[String: Any]] = []
        if path != game.path {
            let oldKey = Self.dataKey(system: system, game: game)
            let renamedGame = DemoLibrary.Game(
                path: path, kind: game.kind, sizeBytes: game.sizeBytes, modifiedAt: game.modifiedAt,
                hasSave: game.hasSave, stateSlots: game.stateSlots, lockedStateCount: game.lockedStateCount,
                cheatCount: game.cheatCount, hasArt: game.hasArt, hasClock: game.hasClock
            )
            lock.withLock {
                guard let s = _library.systems.firstIndex(where: { $0.id == system.id }),
                      let g = _library.systems[s].games.firstIndex(where: { $0.path == game.path })
                else { return }
                _library.systems[s].games[g] = renamedGame
                let newKey = Self.dataKey(system: _library.systems[s], game: renamedGame)
                if let data = gameData[oldKey], gameData[newKey] == nil {
                    gameData[newKey] = data
                    gameData[oldKey] = nil
                }
            }
            renamed.append(["from": "Roms/\(system.folder)/\(game.path)", "to": "Roms/\(system.folder)/\(path)"])
        }
        guard let (newSystem, newGame) = library.game(id: DemoLibrary.id(system.folder + "/" + path)) else {
            return .error(500, code: "internal", message: "Renamed game not found")
        }
        return .json(200, ["game": summaryJSON(system: newSystem, game: newGame), "renamed": renamed])
    }
}
