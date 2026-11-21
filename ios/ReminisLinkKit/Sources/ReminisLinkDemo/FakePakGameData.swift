import CryptoKit
import Foundation
import ReminisLinkKit

// Milestone M5 of the fake pak: saves and their backup copies, state slots,
// locks (with the check the pak runs at start, docs/specs/05 §5.2), the
// resume history, cheats with their etag, and `GET /sd/files`. It follows
// protocol/openapi.yaml; file contents are not kept, only a size and a
// digest (downloads return zero bytes of that size).

/// The content of a file, as far as the fake pak cares.
struct FakeBlob: Equatable {
    var size: Int64
    /// Identifies the content (compared by the lock check).
    var digest: String
    /// The content, kept for files of at most 1 MiB sent to the fake pak
    /// (a save restored from the iPhone is served back as it was sent).
    var bytes: Data?

    init(size: Int64, digest: String, bytes: Data? = nil) {
        self.size = size
        self.digest = digest
        self.bytes = bytes
    }

    static func of(_ data: Data) -> FakeBlob {
        FakeBlob(size: Int64(data.count), digest: Signer.contentSHA256(data), bytes: data.count <= 1 << 20 ? data : nil)
    }

    /// What a download serves: the bytes kept, else `size` bytes starting
    /// with the digest (so that two contents never look the same).
    var content: Data {
        if let bytes { return bytes }
        var data = Data(count: Int(min(size, 64 << 20)))
        let tag = Data(digest.utf8.prefix(data.count))
        data.replaceSubrange(0..<tag.count, with: tag)
        return data
    }
}

struct FakeBackup {
    let id: String
    let label: String?
    let createdAt: Date
    let blob: FakeBlob
    let reason: String

    var json: [String: Any] {
        [
            "id": id, "label": label.map { $0 as Any } ?? NSNull(), "createdAt": ProtocolJSON.formatDate(createdAt),
            "sizeBytes": blob.size, "reason": reason,
        ]
    }
}

struct FakeSlot {
    /// Nil when the state is gone (only the locked copy is left).
    var state: FakeBlob?
    var modifiedAt: Date
    var hasThumbnail: Bool
    var lockCopy: FakeBlob?
    /// `restored` is reported by the next `GET /states`, then `intact`.
    var restoredPending = false
    /// A status set by the demo or a test (`modified`, `core-changed`...).
    var forcedStatus: String?
}

struct FakeResumeEntry {
    let id: String
    let capturedAt: Date
    let blob: FakeBlob
}

struct FakeCheat {
    var description: String
    var code: String
    var enabled: Bool
    var extra: [String: String]

    var json: [String: Any] {
        ["description": description, "code": code, "enabled": enabled, "extra": extra]
    }
}

/// Saves, states and cheats of one game.
struct FakeGameData {
    var save: FakeBlob?
    var saveModifiedAt: Date
    /// The clock file of the save (`.rtc`, milestone M9).
    var rtc: FakeBlob?
    /// Newest first.
    var backups: [FakeBackup] = []
    var slots: [Int: FakeSlot] = [:]
    /// Newest first, at most 5.
    var resume: [FakeResumeEntry] = []
    var cheats: [FakeCheat] = []

    /// The NextUI resume slot (docs/specs/05 §3.1).
    static let resumeSlot = 9
    static let maxAutomaticBackups = 10
    static let maxResumeEntries = 5
}

extension FakePak {
    // MARK: - Paths (informative)

    static func savePath(_ system: DemoLibrary.System, _ game: DemoLibrary.Game) -> String {
        "Saves/\(system.tag)/\(game.fileName).sav"
    }

    static func statePath(_ system: DemoLibrary.System, _ game: DemoLibrary.Game, slot: Int) -> String {
        ".userdata/shared/.minui/\(system.tag)/\(game.fileName).st\(slot)"
    }

    static func cheatsPath(_ system: DemoLibrary.System, _ game: DemoLibrary.Game) -> String {
        "Cheats/\(system.tag)/\(game.fileName).cht"
    }

    // MARK: - Seed

    /// The key of a game's data: on NextUI, games of a system with the
    /// same ROM file name (in different folders) share their save, their
    /// states and their cheats, all named after the ROM file
    /// (`Saves/<TAG>/<rom>.sav`, `<TAG>-<core>/<rom>.st<N>`,
    /// `Cheats/<TAG>/<rom>.cht`; docs/specs/m0/source-analysis.md), so
    /// they share the data of the first of them. Every read and write of
    /// game data goes through this key.
    static func dataKey(system: DemoLibrary.System, game: DemoLibrary.Game) -> String {
        let first = system.games.first { $0.fileName.lowercased() == game.fileName.lowercased() } ?? game
        return DemoLibrary.id(system.folder + "/" + first.path)
    }

    /// The data of a game, made from its library counters the first time.
    func currentData(system: DemoLibrary.System, game: DemoLibrary.Game) -> FakeGameData {
        let id = Self.dataKey(system: system, game: game)
        return lock.withLock {
            if let data = gameData[id] { return data }
            let data = Self.seed(game: game)
            gameData[id] = data
            return data
        }
    }

    static func seed(game: DemoLibrary.Game) -> FakeGameData {
        let base = game.modifiedAt
        var data = FakeGameData(saveModifiedAt: base)
        if game.hasSave {
            data.save = FakeBlob(size: 131_072, digest: "seed-save-\(game.path)")
            if game.hasClock { data.rtc = FakeBlob(size: 16, digest: "rtc-\(game.path)") }
            data.backups = [
                FakeBackup(id: "bk_20261118T170000Z_a1b2", label: "Before the final boss", createdAt: base.addingTimeInterval(-3_600),
                           blob: FakeBlob(size: 131_072, digest: "seed-bk1-\(game.path)"), reason: "manual"),
                FakeBackup(id: "bk_20261117T090000Z_c3d4", label: nil, createdAt: base.addingTimeInterval(-90_000),
                           blob: FakeBlob(size: 131_072, digest: "seed-bk2-\(game.path)"), reason: "before-replace"),
            ]
        }
        for (n, slot) in game.stateSlots.enumerated() {
            let blob = FakeBlob(size: 401_408, digest: "seed-st\(slot)-\(game.path)")
            let locked = n < game.lockedStateCount
            data.slots[slot] = FakeSlot(
                state: blob, modifiedAt: base.addingTimeInterval(Double(-n) * 7_200), hasThumbnail: n % 2 == 0,
                lockCopy: locked ? blob : nil,
                // The first locked slot was overwritten in game and put back
                // at the last start, so the demo shows "Restored" once.
                restoredPending: locked && n == 0,
                // The second locked slot (multi-disc demo game) shows a
                // copy the pak could not restore.
                forcedStatus: locked && n == 1 ? "modified" : nil
            )
        }
        if !game.stateSlots.isEmpty {
            // The resume state NextUI writes when the console sleeps in game.
            data.slots[FakeGameData.resumeSlot] = FakeSlot(
                state: FakeBlob(size: 401_408, digest: "seed-rh0-\(game.path)"), modifiedAt: base,
                hasThumbnail: false, lockCopy: nil
            )
            data.resume = (0..<3).map { i in
                let date = base.addingTimeInterval(Double(-i) * 86_400)
                return FakeResumeEntry(id: "rh_\(Self.stamp(date))", capturedAt: date,
                                       blob: FakeBlob(size: 401_408, digest: "seed-rh\(i)-\(game.path)"))
            }
        }
        data.cheats = (0..<game.cheatCount).map { (i: Int) -> FakeCheat in
            let address: Int = 0x0200_0000 + i * 16
            let code = String(format: "%08X+%04X", address, i)
            let extra: [String: String] = i == 0 ? ["handler": "0"] : [:]
            return FakeCheat(description: "Demo cheat \(i + 1)", code: code, enabled: i == 0, extra: extra)
        }
        return data
    }

    static func stamp(_ date: Date) -> String {
        ProtocolJSON.formatDate(date).replacingOccurrences(of: "-", with: "").replacingOccurrences(of: ":", with: "")
    }

    // MARK: - Test and demo controls

    /// As if the game were played and slot `slot` overwritten (or created).
    public func overwriteStateInGame(gameId: String, slot: Int) {
        guard let (system, game) = library.game(id: gameId) else { return }
        var data = currentData(system: system, game: game)
        let previous = data.slots[slot]
        data.slots[slot] = FakeSlot(
            state: FakeBlob(size: 401_408, digest: Self.randomId("game-")), modifiedAt: Date(),
            hasThumbnail: previous?.hasThumbnail ?? true, lockCopy: previous?.lockCopy
        )
        let key = Self.dataKey(system: system, game: game)
        lock.withLock { gameData[key] = data }
    }

    /// As if the cheat file were changed on the console (another app):
    /// its etag changes.
    public func changeCheatsOnConsole(gameId: String) {
        guard let (system, game) = library.game(id: gameId) else { return }
        var data = currentData(system: system, game: game)
        data.cheats.append(FakeCheat(description: "Added on the console", code: "00000000+0000", enabled: false, extra: [:]))
        let key = Self.dataKey(system: system, game: game)
        lock.withLock { gameData[key] = data }
    }

    /// The pak is opened again: ``restart()``, then the lock check of
    /// docs/specs/05 §5.2 (a changed or missing state gets its locked copy
    /// back, the overwritten one goes to the trash). Returns the restored
    /// slots as `gameId#slot`.
    @discardableResult
    public func restartWithMaintenance() -> [String] {
        restart()
        var restored: [String] = []
        let now = Date()
        lock.withLock {
            for (gameId, var data) in gameData {
                for (slot, var entry) in data.slots {
                    guard let copy = entry.lockCopy, entry.state != copy else { continue }
                    if let overwritten = entry.state {
                        trashEntries.insert([
                            "id": "tr_\(Self.stamp(now))_\(String(format: "%04x", slot))", "trashedAt": ProtocolJSON.formatDate(now),
                            "reason": "lock-restore", "label": "State \(slot)", "files": ["state\(slot)"],
                            "sizeBytes": overwritten.size, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
                        ], at: 0)
                    }
                    entry.state = copy
                    entry.modifiedAt = now
                    entry.restoredPending = true
                    data.slots[slot] = entry
                    restored.append("\(gameId)#\(slot)")
                }
                gameData[gameId] = data
            }
        }
        return restored.sorted()
    }

    #if DEBUG
    /// The next `PUT /cheats` finds the file changed on the console just
    /// before (`412`): for the demo and the UI tests.
    public var conflictOnNextCheatsSave: Bool {
        get { lock.withLock { _conflictOnNextCheatsSave } }
        set { lock.withLock { _conflictOnNextCheatsSave = newValue } }
    }
    #endif

    /// Forces the lock status reported for a slot (nil: computed again).
    public func setLockStatus(gameId: String, slot: Int, _ status: String?) {
        guard let (system, game) = library.game(id: gameId) else { return }
        _ = currentData(system: system, game: game)
        let key = Self.dataKey(system: system, game: game)
        lock.withLock { gameData[key]?.slots[slot]?.forcedStatus = status }
    }

    /// The etag of the cheat list of a game.
    public func cheatsETag(gameId: String) -> String? {
        guard let (system, game) = library.game(id: gameId) else { return nil }
        return Self.etag(currentData(system: system, game: game).cheats)
    }

    // MARK: - Routing

    func routeGameData(_ request: URLRequest, method: String, segments: [String], params: [String: String], body: Data) -> FakeResponse? {
        if method == "GET", segments == ["sd", "files"] {
            return sdFiles(type: params["type"])
        }
        guard segments.count >= 3, segments[0] == "games" else { return nil }
        let gameId = segments[1]
        let rest = Array(segments.dropFirst(2))
        guard let (system, game) = library.game(id: gameId) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        func slotNumber(_ s: String) -> Int? {
            guard let n = Int(s), n >= 0, String(n) == s else { return nil }
            return n
        }
        switch (method, rest) {
        case ("GET", ["save"]):
            return downloadSave(system: system, game: game, gameId: gameId)
        case ("GET", ["save", "rtc"]):
            guard let rtc = currentData(system: system, game: game).rtc else {
                return .error(404, code: "not_found", message: "No clock file")
            }
            return fileResponse(name: "\(game.fileName).rtc", content: rtc.content)
        case ("PUT", ["save"]):
            guard body.count <= 64 << 20 else { return .error(413, code: "too_large", message: "Body too large") }
            return .json(200, ["backup": replaceSave(gameId: gameId, with: .of(body)).map { $0 as Any } ?? NSNull()])
        case ("GET", ["save", "backups"]):
            return .json(200, ["backups": currentData(system: system, game: game).backups.map(\.json)])
        case ("POST", ["save", "backups"]):
            return createBackup(system: system, game: game, gameId: gameId, body: body)
        case ("GET", let r) where r.count == 3 && r[0] == "save" && r[1] == "backups":
            guard let backup = currentData(system: system, game: game).backups.first(where: { $0.id == r[2] }) else {
                return .error(404, code: "not_found", message: "Backup not found")
            }
            return fileResponse(name: "\(game.fileName).\(backup.id).sav", size: backup.blob.size)
        case ("DELETE", let r) where r.count == 3 && r[0] == "save" && r[1] == "backups":
            return update(gameId: gameId, system: system, game: game) { data in
                guard let index = data.backups.firstIndex(where: { $0.id == r[2] }) else {
                    return .error(404, code: "not_found", message: "Backup not found")
                }
                let backup = data.backups.remove(at: index)
                // Never erased: to the trash.
                _ = trash(reason: "delete-backup", label: game.name,
                          files: [".userdata/shared/ReminisLink/backups/\(system.tag)/\(game.fileName)/saves/\(backup.id).sav"],
                          bytes: backup.blob.size, payload: .backup(key: Self.dataKey(system: system, game: game), backup: backup))
                return FakeResponse(status: 204)
            }
        case ("POST", let r) where r.count == 4 && r[0] == "save" && r[1] == "backups" && r[3] == "restore":
            return restoreBackup(system: system, game: game, gameId: gameId, backupId: r[2])
        case ("GET", ["states"]):
            return listStates(system: system, game: game, gameId: gameId)
        case ("GET", let r) where r.count == 2 && r[0] == "states":
            guard let slot = slotNumber(r[1]) else { return .error(400, code: "invalid_request", message: "Invalid slot") }
            guard let state = currentData(system: system, game: game).slots[slot]?.state else {
                return .error(404, code: "not_found", message: "State not found")
            }
            return fileResponse(name: "\(game.fileName).st\(slot)", size: state.size)
        case ("DELETE", let r) where r.count == 2 && r[0] == "states":
            guard let slot = slotNumber(r[1]) else { return .error(400, code: "invalid_request", message: "Invalid slot") }
            return deleteState(system: system, game: game, gameId: gameId, slot: slot)
        case ("GET", let r) where r.count == 3 && r[0] == "states" && r[2] == "thumbnail":
            guard let slot = slotNumber(r[1]), let entry = currentData(system: system, game: game).slots[slot],
                  entry.state != nil, entry.hasThumbnail
            else { return .error(404, code: "not_found", message: "No thumbnail") }
            return FakeResponse(status: 200, headers: ["Content-Type": "image/png"], body: stateThumbnail(game: game, slot: slot))
        case (let m, let r) where r.count == 3 && r[0] == "states" && r[2] == "lock" && (m == "PUT" || m == "DELETE"):
            guard let slot = slotNumber(r[1]) else { return .error(400, code: "invalid_request", message: "Invalid slot") }
            return m == "PUT" ? lockState(system: system, game: game, gameId: gameId, slot: slot)
                : unlockState(system: system, game: game, gameId: gameId, slot: slot)
        case ("POST", let r) where r.count == 3 && r[0] == "states" && r[2] == "restore":
            guard let slot = slotNumber(r[1]) else { return .error(400, code: "invalid_request", message: "Invalid slot") }
            return restoreLocked(system: system, game: game, gameId: gameId, slot: slot)
        case ("GET", ["resume-history"]):
            return .json(200, ["entries": currentData(system: system, game: game).resume.map(Self.resumeJSON)])
        case ("POST", let r) where r.count == 3 && r[0] == "resume-history" && r[2] == "restore":
            return restoreResume(system: system, game: game, gameId: gameId, entryId: r[1])
        case ("GET", ["cheats"]):
            let cheats = currentData(system: system, game: game).cheats
            let etag = Self.etag(cheats)
            return .json(200, ["cheats": Self.cheatsJSON(cheats), "etag": etag], headers: ["ETag": etag])
        case ("PUT", ["cheats"]):
            return replaceCheats(system: system, game: game, gameId: gameId, ifMatch: request.value(forHTTPHeaderField: "If-Match"), body: body)
        case ("POST", ["cheats", "import"]):
            return importCheats(system: system, game: game, gameId: gameId, body: body)
        default:
            return nil
        }
    }

    /// Applies a change to the data of a game, under the lock.
    private func update(gameId: String, system: DemoLibrary.System, game: DemoLibrary.Game,
                        _ change: (inout FakeGameData) -> FakeResponse) -> FakeResponse {
        _ = currentData(system: system, game: game)
        let key = Self.dataKey(system: system, game: game)
        return lock.withLock {
            var data = gameData[key] ?? Self.seed(game: game)
            let response = change(&data)
            gameData[key] = data
            return response
        }
    }

    private func fileResponse(name: String, size: Int64) -> FakeResponse {
        fileResponse(name: name, content: Data(count: Int(min(size, 64 << 20))))
    }

    func fileResponse(name: String, content: Data) -> FakeResponse {
        FakeResponse(status: 200, headers: [
            "Content-Type": "application/octet-stream",
            "Content-Disposition": "attachment; filename=\"\(name.replacingOccurrences(of: "\"", with: "\\\""))\"",
        ], body: content)
    }

    private func trash(reason: String, label: String, files: [String], bytes: Int64, payload: FakeTrashPayload? = nil) -> [String: Any] {
        let now = Date()
        let id = "tr_\(Self.stamp(now))_\(UUID().uuidString.prefix(4).lowercased())"
        let entry: [String: Any] = [
            "id": id, "trashedAt": ProtocolJSON.formatDate(now), "reason": reason, "label": label,
            "files": files, "sizeBytes": bytes, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
        ]
        trashEntries.insert(entry, at: 0)
        trashPayloads[id] = payload
        return ["trashEntryId": id, "moved": files, "movedBytes": bytes]
    }

    // MARK: - Saves

    private func downloadSave(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String) -> FakeResponse {
        guard let save = currentData(system: system, game: game).save else {
            return .error(404, code: "not_found", message: "No save")
        }
        return fileResponse(name: "\(game.fileName).sav", content: save.content)
    }

    /// Adds a backup copy (automatic ones are limited to 10 per game).
    private static func addBackup(_ data: inout FakeGameData, blob: FakeBlob, label: String?, reason: String) -> FakeBackup {
        let now = Date()
        let backup = FakeBackup(
            id: "bk_\(stamp(now))_\(UUID().uuidString.prefix(4).lowercased())",
            label: label, createdAt: now, blob: blob, reason: reason
        )
        data.backups.insert(backup, at: 0)
        if reason != "manual" {
            // The oldest automatic copies beyond 10 are dropped (the real
            // pak moves them to the trash, `delete-backup`).
            let automatic = data.backups.enumerated().filter { $0.element.reason != "manual" }.map(\.offset)
            for index in automatic.dropFirst(FakeGameData.maxAutomaticBackups).reversed() {
                data.backups.remove(at: index)
            }
        }
        return backup
    }

    /// Replaces the save, copying the previous one first
    /// (`before-replace`, label "Avant remplacement"). Returns that copy.
    func replaceSave(gameId: String, with blob: FakeBlob) -> [String: Any]? {
        guard let (system, game) = library.game(id: gameId) else { return nil }
        var result: [String: Any]?
        _ = update(gameId: gameId, system: system, game: game) { data in
            if let previous = data.save {
                result = Self.addBackup(&data, blob: previous, label: "Avant remplacement", reason: "before-replace").json
            }
            data.save = blob
            data.saveModifiedAt = Date()
            return FakeResponse(status: 200)
        }
        return result
    }

    private func createBackup(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, body: Data) -> FakeResponse {
        var label: String?
        if !body.isEmpty {
            guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any] else {
                return .error(400, code: "invalid_request", message: "Invalid JSON")
            }
            label = object["label"] as? String
            if let label, label.unicodeScalars.count > BackupLabel.maxLength {
                return .error(400, code: "invalid_request", message: "Label too long")
            }
        }
        return update(gameId: gameId, system: system, game: game) { data in
            guard let save = data.save else { return .error(404, code: "not_found", message: "No save") }
            return .json(201, Self.addBackup(&data, blob: save, label: label, reason: "manual").json)
        }
    }

    private func restoreBackup(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, backupId: String) -> FakeResponse {
        update(gameId: gameId, system: system, game: game) { data in
            guard let backup = data.backups.first(where: { $0.id == backupId }) else {
                return .error(404, code: "not_found", message: "Backup not found")
            }
            var copy: Any = NSNull()
            if let current = data.save {
                copy = Self.addBackup(&data, blob: current, label: nil, reason: "before-restore").json
            }
            data.save = backup.blob
            data.saveModifiedAt = Date()
            return .json(200, ["backup": copy])
        }
    }

    // MARK: - States

    private static func slotJSON(_ slot: Int, _ entry: FakeSlot, reportRestored: Bool = true) -> [String: Any] {
        let size = entry.state?.size ?? entry.lockCopy?.size ?? 0
        var status: Any = NSNull()
        if entry.lockCopy != nil, let forced = entry.forcedStatus {
            status = forced
        } else if entry.lockCopy != nil {
            if entry.state == nil {
                status = "missing"
            } else if entry.restoredPending && reportRestored {
                status = "restored"
            } else {
                status = "intact"
            }
        }
        return [
            "slot": slot, "sizeBytes": size, "modifiedAt": ProtocolJSON.formatDate(entry.modifiedAt),
            "hasThumbnail": entry.state != nil && entry.hasThumbnail, "locked": entry.lockCopy != nil, "lockStatus": status,
        ]
    }

    /// `restored` is reported once, then the slot is `intact` again.
    private func listStates(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String) -> FakeResponse {
        update(gameId: gameId, system: system, game: game) { data in
            let slots = data.slots.keys.sorted().compactMap { slot -> [String: Any]? in
                guard let entry = data.slots[slot] else { return nil }
                return Self.slotJSON(slot, entry)
            }
            for slot in data.slots.keys { data.slots[slot]?.restoredPending = false }
            let resume: Any = data.slots.isEmpty && data.resume.isEmpty ? NSNull() : FakeGameData.resumeSlot
            return .json(200, ["slots": slots, "resumeSlot": resume])
        }
    }

    /// Writes an uploaded state (refused when the slot is locked).
    func storeState(gameId: String, slot: Int, blob: FakeBlob) -> [String: Any]? {
        guard let (system, game) = library.game(id: gameId) else { return nil }
        var json: [String: Any]?
        _ = update(gameId: gameId, system: system, game: game) { data in
            if data.slots[slot]?.lockCopy != nil { return FakeResponse(status: 409) }
            let entry = FakeSlot(state: blob, modifiedAt: Date(), hasThumbnail: false, lockCopy: nil)
            data.slots[slot] = entry
            json = Self.slotJSON(slot, entry)
            return FakeResponse(status: 200)
        }
        return json
    }

    private func deleteState(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, slot: Int) -> FakeResponse {
        update(gameId: gameId, system: system, game: game) { data in
            guard let entry = data.slots[slot], let state = entry.state else {
                return .error(404, code: "not_found", message: "State not found")
            }
            guard entry.lockCopy == nil else { return .error(409, code: "state_locked", message: "State is locked") }
            data.slots[slot] = nil
            return .json(200, trash(reason: "delete-state", label: game.name,
                                    files: [Self.statePath(system, game, slot: slot)], bytes: state.size,
                                    payload: .state(key: Self.dataKey(system: system, game: game), slot: slot, entry: entry)))
        }
    }

    private func lockState(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, slot: Int) -> FakeResponse {
        update(gameId: gameId, system: system, game: game) { data in
            guard var entry = data.slots[slot], let state = entry.state else {
                return .error(404, code: "not_found", message: "State not found")
            }
            entry.lockCopy = state
            entry.forcedStatus = nil
            entry.restoredPending = false
            data.slots[slot] = entry
            return .json(200, Self.slotJSON(slot, entry))
        }
    }

    private func unlockState(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, slot: Int) -> FakeResponse {
        update(gameId: gameId, system: system, game: game) { data in
            guard var entry = data.slots[slot] else { return .error(404, code: "not_found", message: "State not found") }
            // A copy that differs from the state (or is its only version)
            // goes to the trash; an identical one is erased.
            if let copy = entry.lockCopy, entry.state != copy || entry.forcedStatus != nil {
                _ = trash(reason: "overwrite", label: game.name, files: ["lock slot\(slot)"], bytes: copy.size)
            }
            entry.lockCopy = nil
            entry.forcedStatus = nil
            entry.restoredPending = false
            // A missing state with no locked copy left: the slot is empty.
            data.slots[slot] = entry.state == nil ? nil : entry
            return .json(200, Self.slotJSON(slot, entry))
        }
    }

    private func restoreLocked(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, slot: Int) -> FakeResponse {
        update(gameId: gameId, system: system, game: game) { data in
            guard var entry = data.slots[slot], let copy = entry.lockCopy else {
                return .error(404, code: "not_found", message: "No locked copy")
            }
            if entry.forcedStatus == "core-changed" {
                return .error(409, code: "conflict", message: "Locked copy comes from another core")
            }
            entry.forcedStatus = nil
            if let current = entry.state, current != copy {
                _ = trash(reason: "lock-restore", label: game.name, files: [Self.statePath(system, game, slot: slot)], bytes: current.size)
            }
            entry.state = copy
            entry.modifiedAt = Date()
            entry.restoredPending = false
            data.slots[slot] = entry
            return .json(200, Self.slotJSON(slot, entry))
        }
    }

    private static func resumeJSON(_ entry: FakeResumeEntry) -> [String: Any] {
        ["id": entry.id, "capturedAt": ProtocolJSON.formatDate(entry.capturedAt), "sizeBytes": entry.blob.size]
    }

    /// Archives the current resume state (if not already the newest entry),
    /// then puts the chosen version in its place.
    private func restoreResume(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, entryId: String) -> FakeResponse {
        update(gameId: gameId, system: system, game: game) { data in
            guard let entry = data.resume.first(where: { $0.id == entryId }) else {
                return .error(404, code: "not_found", message: "Entry not found")
            }
            let slot = FakeGameData.resumeSlot
            let now = Date()
            if let current = data.slots[slot]?.state, data.resume.first?.blob != current {
                data.resume.insert(FakeResumeEntry(id: "rh_\(Self.stamp(now))", capturedAt: now, blob: current), at: 0)
                data.resume = Array(data.resume.prefix(FakeGameData.maxResumeEntries))
            }
            var target = data.slots[slot] ?? FakeSlot(state: nil, modifiedAt: now, hasThumbnail: false, lockCopy: nil)
            target.state = entry.blob
            target.modifiedAt = now
            data.slots[slot] = target
            return .json(200, ["entries": data.resume.map(Self.resumeJSON)])
        }
    }

    // MARK: - Cheats

    static func etag(_ cheats: [FakeCheat]) -> String {
        let body = (try? JSONSerialization.data(withJSONObject: cheatsJSON(cheats), options: [.sortedKeys])) ?? Data()
        return "\"sha256-\(Signer.contentSHA256(body).prefix(16))\""
    }

    static func cheatsJSON(_ cheats: [FakeCheat]) -> [[String: Any]] {
        cheats.enumerated().map { index, cheat in
            var json = cheat.json
            json["index"] = index
            return json
        }
    }

    private static func parseCheats(_ list: Any?) -> [FakeCheat]? {
        guard let items = list as? [[String: Any]] else { return nil }
        var out: [FakeCheat] = []
        for item in items {
            guard let description = item["description"] as? String, let code = item["code"] as? String,
                  let enabled = item["enabled"] as? Bool
            else { return nil }
            // Keys come without their `cheat<N>_` prefix; a prefixed key is
            // accepted, its prefix dropped.
            var extra: [String: String] = [:]
            for (key, value) in item["extra"] as? [String: String] ?? [:] {
                extra[Self.unprefixed(key)] = value
            }
            out.append(FakeCheat(description: description, code: code, enabled: enabled, extra: extra))
        }
        return out
    }

    private func replaceCheats(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, ifMatch: String?, body: Data) -> FakeResponse {
        guard let ifMatch else { return .error(400, code: "invalid_request", message: "Missing If-Match") }
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let cheats = Self.parseCheats(object["cheats"])
        else { return .error(400, code: "invalid_request", message: "Invalid cheats") }
        let conflict = lock.withLock { () -> Bool in
            defer { _conflictOnNextCheatsSave = false }
            return _conflictOnNextCheatsSave
        }
        if conflict { changeCheatsOnConsole(gameId: gameId) }
        return update(gameId: gameId, system: system, game: game) { data in
            guard Self.etag(data.cheats) == ifMatch else {
                return .error(412, code: "conflict", message: "Cheat file changed")
            }
            data.cheats = cheats
            let etag = Self.etag(cheats)
            return .json(200, ["cheats": Self.cheatsJSON(cheats), "etag": etag], headers: ["ETag": etag])
        }
    }

    /// `cheat12_handler` → `handler`.
    static func unprefixed(_ key: String) -> String {
        guard let range = key.range(of: "^cheat[0-9]+_", options: .regularExpression) else { return key }
        return String(key[range.upperBound...])
    }

    /// Reads the cheats of a RetroArch `.cht` text: `cheatN_desc`,
    /// `cheatN_code`, `cheatN_enable`; other `cheatN_*` keys go to `extra`.
    static func parseCHT(_ text: String) -> [FakeCheat] {
        var byIndex: [Int: [String: String]] = [:]
        for line in text.split(whereSeparator: \.isNewline) {
            let parts = line.split(separator: "=", maxSplits: 1)
            guard parts.count == 2 else { continue }
            let key = parts[0].trimmingCharacters(in: .whitespaces)
            var value = parts[1].trimmingCharacters(in: .whitespaces)
            if value.hasPrefix("\""), value.hasSuffix("\""), value.count >= 2 { value = String(value.dropFirst().dropLast()) }
            guard key.hasPrefix("cheat"), let underscore = key.firstIndex(of: "_"),
                  let index = Int(key[key.index(key.startIndex, offsetBy: 5)..<underscore])
            else { continue }
            byIndex[index, default: [:]][key] = value
        }
        return byIndex.keys.sorted().compactMap { index in
            guard var keys = byIndex[index], let code = keys.removeValue(forKey: "cheat\(index)_code") else { return nil }
            let description = keys.removeValue(forKey: "cheat\(index)_desc") ?? ""
            let enabled = keys.removeValue(forKey: "cheat\(index)_enable") == "true"
            var extra: [String: String] = [:]
            for (key, value) in keys { extra[unprefixed(key)] = value }
            return FakeCheat(description: description, code: code, enabled: enabled, extra: extra)
        }
    }

    private func sdFileList() -> [(id: String, path: String, text: String)] {
        var files: [(String, String, String)] = []
        for system in library.systems {
            for game in system.games where game.cheatCount > 0 {
                let path = Self.cheatsPath(system, game)
                files.append(("sd_" + DemoLibrary.id(path), path, """
                cheats = 2
                cheat0_desc = "SD cheat A"
                cheat0_code = "02000100+0001"
                cheat0_enable = false
                cheat1_desc = "SD cheat B"
                cheat1_code = "02000200+0002"
                cheat1_enable = false
                cheat1_handler = "1"
                """))
            }
        }
        let shared = "Cheats/HA/Shared Pack.cht"
        files.append(("sd_" + DemoLibrary.id(shared), shared, "cheats = 1\ncheat0_desc = \"Shared\"\ncheat0_code = \"02000300+0003\"\n"))
        let broken = "Cheats/HA/Notes.cht"
        files.append(("sd_" + DemoLibrary.id(broken), broken, "not a cheat file\n"))
        return files
    }

    private func sdFiles(type: String?) -> FakeResponse {
        switch type {
        case "cht":
            return .json(200, ["files": sdFileList().map { ["id": $0.id, "path": $0.path, "sizeBytes": Int64($0.text.utf8.count)] }])
        case "patch":
            return .json(200, ["files": sdPatchFiles().map { ["id": $0.id, "path": $0.path, "sizeBytes": Int64($0.bytes.count)] }])
        default:
            return .error(400, code: "invalid_request", message: "Invalid type")
        }
    }

    private func importCheats(system: DemoLibrary.System, game: DemoLibrary.Game, gameId: String, body: Data) -> FakeResponse {
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let source = object["source"] as? [String: Any], let type = source["type"] as? String,
              let mode = object["mode"] as? String, mode == "merge" || mode == "replace"
        else { return .error(400, code: "invalid_request", message: "Invalid import request") }
        let text: String
        switch type {
        case "sd":
            guard let id = source["sdFileId"] as? String, let file = sdFileList().first(where: { $0.id == id }) else {
                return .error(404, code: "not_found", message: "File not found")
            }
            text = file.text
        case "inline":
            guard let inline = source["text"] as? String else { return .error(400, code: "invalid_request", message: "Missing text") }
            text = inline
        case "upload", "staging":
            let key = type == "upload" ? "uploadId" : "stagingId"
            guard let id = source[key] as? String, let data = lock.withLock({ stagedUploads[id] }) else {
                return .error(404, code: "not_found", message: "Staging file not found")
            }
            text = String(decoding: data, as: UTF8.self)
        default:
            return .error(400, code: "invalid_request", message: "Unknown source type")
        }
        let imported = Self.parseCHT(text)
        guard !imported.isEmpty else { return .error(422, code: "unsupported_format", message: "No cheat found") }
        return update(gameId: gameId, system: system, game: game) { data in
            data.cheats = mode == "replace" ? imported : data.cheats + imported
            return .json(200, ["imported": imported.count, "total": data.cheats.count])
        }
    }
}
