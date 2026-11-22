import Compression
import Foundation
import ReminisLinkKit

// Milestone M19 of the fake pak: `GET /states` (every state with its
// SHA-256), the game archives (`GET /games/{gameId}/archive`,
// `GET /archives/{stagingId}`, `POST /archives/{stagingId}/import`). The
// archives are real zips (stored entries) with the manifest of the
// contract, so an archive exported from one fake pak imports into another.

/// What an `archive-import` job imports.
struct FakeArchiveImport {
    let stagingId: String
    let manifest: GameArchiveManifest
    /// The bytes of each file, by manifest path.
    let files: [String: Data]
    let target: ArchiveTarget
}

/// A minimal zip writer and reader (stored entries only).
enum FakeZip {
    /// `deflate`: entries are deflated (as the real pak does for saves,
    /// states, cheats and the manifest).
    static func write(_ entries: [(name: String, data: Data)], deflate: Bool = false) -> Data {
        var out = Data()
        var central = Data()
        for entry in entries {
            let name = Data(entry.name.utf8)
            let crc = crc32(entry.data)
            let offset = UInt32(out.count)
            let packed = deflate ? ((try? (entry.data as NSData).compressed(using: .zlib) as Data) ?? entry.data) : entry.data
            let method: UInt16 = deflate && packed != entry.data ? 8 : 0
            let body = method == 8 ? packed : entry.data
            var local = Data()
            local.append(le32(0x0403_4b50))
            local.append(le16(20)); local.append(le16(0x0800)); local.append(le16(method)) // version, UTF-8 flag, method
            local.append(le16(0)); local.append(le16(0x21)) // time, date (1980-01-01)
            local.append(le32(crc)); local.append(le32(UInt32(body.count))); local.append(le32(UInt32(entry.data.count)))
            local.append(le16(UInt16(name.count))); local.append(le16(0))
            local.append(name)
            out.append(local)
            out.append(body)
            central.append(le32(0x0201_4b50))
            central.append(le16(20)); central.append(le16(20)); central.append(le16(0x0800)); central.append(le16(method))
            central.append(le16(0)); central.append(le16(0x21))
            central.append(le32(crc)); central.append(le32(UInt32(body.count))); central.append(le32(UInt32(entry.data.count)))
            central.append(le16(UInt16(name.count))); central.append(le16(0)); central.append(le16(0))
            central.append(le16(0)); central.append(le16(0)); central.append(le32(0o100644 << 16))
            central.append(le32(offset))
            central.append(name)
        }
        let centralOffset = UInt32(out.count)
        out.append(central)
        out.append(le32(0x0605_4b50))
        out.append(le16(0)); out.append(le16(0))
        out.append(le16(UInt16(entries.count))); out.append(le16(UInt16(entries.count)))
        out.append(le32(UInt32(central.count))); out.append(le32(centralOffset)); out.append(le16(0))
        return out
    }

    /// The entries of a zip (stored or deflated entries, sizes from the
    /// central directory, as the real pak exports them), nil when it is
    /// not one or is damaged: never traps on a truncated or hostile file.
    static func read(_ data: Data) -> [String: Data]? {
        let bytes = [UInt8](data)
        func u16(_ i: Int) -> Int? { i >= 0 && i + 2 <= bytes.count ? Int(bytes[i]) | Int(bytes[i + 1]) << 8 : nil }
        func u32(_ i: Int) -> Int? {
            guard let lo = u16(i), let hi = u16(i + 2) else { return nil }
            return lo | hi << 16
        }
        guard bytes.count >= 22 else { return nil }
        let lowest = max(0, bytes.count - 22 - 65_535)
        guard let end = stride(from: bytes.count - 22, through: lowest, by: -1).first(where: { u32($0) == 0x0605_4b50 }),
              let count = u16(end + 10), var p = u32(end + 16), count <= 512 else { return nil }
        var entries: [String: Data] = [:]
        // All the entries together stay under the staging limit of the
        // demo, so the archive never holds more than that in memory (M19
        // final review).
        var total = 0
        for _ in 0..<count {
            guard u32(p) == 0x0201_4b50, let method = u16(p + 10), let compressed = u32(p + 20), let size = u32(p + 24),
                  let nameLength = u16(p + 28), let extra = u16(p + 30), let comment = u16(p + 32), let local = u32(p + 42),
                  method == 0 || method == 8, size <= 64 << 20,
                  p + 46 + nameLength <= bytes.count,
                  let name = String(bytes: bytes[(p + 46)..<(p + 46 + nameLength)], encoding: .utf8),
                  u32(local) == 0x0403_4b50, let localName = u16(local + 26), let localExtra = u16(local + 28)
            else { return nil }
            let start = local + 30 + localName + localExtra
            guard start >= 0, compressed >= 0, start <= bytes.count, compressed <= bytes.count - start, entries[name] == nil else { return nil }
            total += size
            guard total <= Int(FakePak.maxKeptArchiveBytes) else { return nil }
            let raw = Data(bytes[start..<(start + compressed)])
            if method == 0 {
                guard compressed == size else { return nil }
                entries[name] = raw
            } else {
                guard let inflated = inflate(raw, size: size) else { return nil }
                entries[name] = inflated
            }
            p += 46 + nameLength + extra + comment
        }
        return entries
    }

    /// Raw DEFLATE data inflated to exactly `size` bytes, nil otherwise.
    /// Streamed: the output is never allowed to grow much beyond `size`
    /// (a small archive may hold a stream that inflates to far more; Codex
    /// review), the inflation stops as soon as it does.
    private static func inflate(_ raw: Data, size: Int) -> Data? {
        if size == 0 { return raw.isEmpty || raw == Data([0x03, 0x00]) ? Data() : nil }
        struct TooLarge: Error {}
        var out = Data()
        do {
            let filter = try OutputFilter(.decompress, using: .zlib, bufferCapacity: 16 << 10) { chunk in
                guard let chunk else { return }
                if out.count + chunk.count > size { throw TooLarge() }
                out.append(chunk)
            }
            var at = raw.startIndex
            while at < raw.endIndex {
                let end = raw.index(at, offsetBy: min(4096, raw.distance(from: at, to: raw.endIndex)))
                try filter.write(raw[at..<end])
                at = end
            }
            try filter.finalize()
        } catch {
            return nil
        }
        return out.count == size ? out : nil
    }

    private static let crcTable: [UInt32] = (0..<256).map { n in
        var c = UInt32(n)
        for _ in 0..<8 { c = c & 1 != 0 ? (c >> 1) ^ 0xEDB8_8320 : c >> 1 }
        return c
    }

    static func crc32(_ data: Data) -> UInt32 {
        let table = crcTable
        var crc: UInt32 = 0xFFFF_FFFF
        data.withUnsafeBytes { raw in
            for byte in raw.bindMemory(to: UInt8.self) {
                crc = table[Int((crc ^ UInt32(byte)) & 0xFF)] ^ (crc >> 8)
            }
        }
        return ~crc
    }

    private static func le16(_ v: UInt16) -> Data { Data([UInt8(v & 0xFF), UInt8(v >> 8)]) }
    private static func le32(_ v: UInt32) -> Data { Data((0..<4).map { UInt8((v >> (8 * $0)) & 0xFF) }) }
}

extension FakePak {
    /// Largest staging upload whose bytes are kept when it is a game
    /// archive (`*.reminislink.zip`), so that it can be imported.
    static let maxKeptArchiveBytes: Int64 = 64 << 20

    // MARK: - GET /states

    func listAllStates() -> FakeResponse {
        var states: [[String: Any]] = []
        for system in library.systems {
            for game in system.games {
                let data = currentData(system: system, game: game)
                for slot in data.slots.keys.sorted() {
                    guard let entry = data.slots[slot], let state = entry.state else { continue }
                    let thumbnail: Any = entry.hasThumbnail ? { () -> [String: Any] in
                        let png = stateThumbnail(game: game, slot: slot)
                        return ["sizeBytes": png.count, "sha256": Signer.contentSHA256(png)]
                    }() : NSNull()
                    states.append([
                        "gameId": DemoLibrary.id(system.folder + "/" + game.path), "systemId": system.id,
                        "gameName": game.name, "fileName": game.fileName, "slot": slot,
                        "path": Self.statePath(system, game, slot: slot), "sizeBytes": state.size,
                        "modifiedAt": ProtocolJSON.formatDate(entry.modifiedAt), "sha256": Signer.contentSHA256(state.content),
                        "thumbnail": thumbnail,
                    ])
                }
            }
        }
        return .json(200, ["states": states, "truncated": false, "generatedAt": ProtocolJSON.formatDate(Date())])
    }

    // MARK: - Export

    func exportArchive(gameId: String, params: [String: String]) -> FakeResponse {
        let rom: Bool
        switch params["rom"] {
        case nil, "false": rom = false
        case "true": rom = true
        default: return .error(400, code: "invalid_request", message: "Invalid rom")
        }
        guard let (system, game) = library.game(id: gameId) else { return .error(404, code: "not_found", message: "Game not found") }
        if rom && game.kind != .file {
            return .error(422, code: "unsupported_format", message: "Only the ROM of a single-file game can be exported")
        }
        let data = currentData(system: system, game: game)
        var files: [(role: ArchiveRole, slot: Int?, name: String, data: Data)] = []
        if rom { files.append((.rom, nil, game.fileName, Self.madeUpContent(size: min(game.sizeBytes, 8 << 20), tag: game.path))) }
        if let save = data.save { files.append((.save, nil, "\(game.fileName).sav", save.content)) }
        if let rtc = data.rtc { files.append((.rtc, nil, "\(game.fileName).rtc", rtc.content)) }
        for slot in data.slots.keys.sorted() {
            guard let entry = data.slots[slot], let state = entry.state else { continue }
            files.append((.state, slot, "\(game.fileName).st\(slot)", state.content))
            if entry.hasThumbnail { files.append((.stateThumbnail, slot, "thumbnail.png", stateThumbnail(game: game, slot: slot))) }
        }
        if !data.cheats.isEmpty { files.append((.cheats, nil, "\(game.fileName).cht", Self.chtText(data.cheats))) }
        if let art = currentArt(gameId: gameId, game: game) { files.append((.art, nil, "\(game.name).png", art.data)) }
        guard !files.isEmpty else { return .error(404, code: "not_found", message: "Nothing to export") }

        var entries: [(name: String, data: Data)] = []
        var listed: [GameArchiveFile] = []
        for file in files {
            let folder = file.slot.map { "states/\($0)" } ?? file.role.rawValue
            let path = "\(folder)/\(file.name)"
            entries.append((path, file.data))
            listed.append(GameArchiveFile(path: path, role: file.role, slot: file.slot, sizeBytes: Int64(file.data.count), sha256: Signer.contentSHA256(file.data)))
        }
        let manifest = GameArchiveManifest(
            createdAt: Date(),
            source: GameArchiveSource(
                deviceName: deviceName, os: "nextui", platform: "tg5040", pakVersion: pakVersion,
                systemTag: system.tag, systemLabel: system.label, gameName: game.name, fileName: game.fileName,
                romName: game.fileName, kind: game.kind.rawValue
            ),
            files: listed
        )
        guard let json = try? ProtocolJSON.encoder().encode(manifest) else { return .error(500, code: "internal", message: "Internal error") }
        entries.append(("manifest.json", json))
        let name = game.name + GameArchiveManifest.fileSuffix
        return FakeResponse(status: 200, headers: [
            "Content-Type": "application/zip",
            "Content-Disposition": "attachment; filename=\"\(name.replacingOccurrences(of: "\"", with: "\\\""))\"",
        ], body: FakeZip.write(entries))
    }

    // MARK: - Inspect and import

    /// The checked archive of a staging entry (manifest and files).
    private func stagedArchive(_ stagingId: String) -> (manifest: GameArchiveManifest, files: [String: Data])? {
        guard let bytes = lock.withLock({ stagedFiles[stagingId]?.bytes }), let entries = FakeZip.read(bytes),
              let json = entries["manifest.json"],
              let manifest = try? ProtocolJSON.decoder().decode(GameArchiveManifest.self, from: json),
              manifest.format == GameArchiveManifest.formatName, manifest.version == 1,
              Set(entries.keys.filter { $0 != "manifest.json" }) == Set(manifest.files.map(\.path)),
              manifest.files.allSatisfy({ entries[$0.path].map(Signer.contentSHA256) == $0.sha256 })
        else { return nil }
        return (manifest, entries)
    }

    private enum ResolvedTarget {
        case game(DemoLibrary.System, DemoLibrary.Game)
        case newGame(DemoLibrary.System, fileName: String)
    }

    private func resolve(_ target: ArchiveTarget, manifest: GameArchiveManifest) -> Result<ResolvedTarget, FakeResponseBox> {
        switch target {
        case .game(let gameId):
            guard let (system, game) = library.game(id: gameId) else { return .failure(.init(.error(404, code: "not_found", message: "Game not found"))) }
            return .success(.game(system, game))
        case .newGame(let systemId, _):
            guard let system = library.systems.first(where: { $0.id == systemId }) else {
                return .failure(.init(.error(404, code: "not_found", message: "System not found")))
            }
            guard manifest.hasROM else {
                return .failure(.init(.error(422, code: "unsupported_format", message: "The archive holds no ROM: choose a game as the target")))
            }
            let name = manifest.source.fileName
            if let existing = system.games.first(where: { $0.path.lowercased() == name.lowercased() }) {
                return .failure(.init(.error(409, code: "conflict", message: "Target exists", details: [
                    "existing": "Roms/\(system.folder)/\(existing.path)", "gameId": DemoLibrary.id(system.folder + "/" + existing.path),
                ])))
            }
            return .success(.newGame(system, fileName: name))
        }
    }

    private func suggest(_ manifest: GameArchiveManifest) -> ArchiveTarget? {
        let tagged = library.systems.filter { $0.tag.lowercased() == manifest.source.systemTag.lowercased() }
        for system in tagged {
            if let game = system.games.first(where: { $0.fileName.lowercased() == manifest.source.romName.lowercased() }) {
                return .game(gameId: DemoLibrary.id(system.folder + "/" + game.path))
            }
        }
        if manifest.hasROM, let system = tagged.first { return .newGame(systemId: system.id, folderId: nil) }
        return nil
    }

    /// What the import does with each file of the manifest.
    private func plan(_ manifest: GameArchiveManifest, _ target: ResolvedTarget) -> [ArchivePlanEntry] {
        var data = FakeGameData(saveModifiedAt: Date())
        var newGame = true
        var hasArt = false
        if case .game(let system, let game) = target {
            data = currentData(system: system, game: game)
            newGame = false
            hasArt = currentArt(gameId: DemoLibrary.id(system.folder + "/" + game.path), game: game) != nil
        }
        var stateOK = Set<Int>()
        var entries: [ArchivePlanEntry] = []
        func entry(_ file: GameArchiveFile, exists: Bool) -> ArchivePlanEntry {
            ArchivePlanEntry(role: file.role, slot: file.slot, sizeBytes: file.sizeBytes, action: exists ? .replace : .create, reason: nil)
        }
        func skip(_ file: GameArchiveFile, _ reason: ArchiveSkipReason) -> ArchivePlanEntry {
            ArchivePlanEntry(role: file.role, slot: file.slot, sizeBytes: file.sizeBytes, action: .skip, reason: reason)
        }
        for file in manifest.files where file.role == .state {
            let slot = file.slot ?? 0
            if data.slots[slot]?.lockCopy != nil { continue }
            stateOK.insert(slot)
        }
        for file in manifest.files {
            switch file.role {
            case .rom: entries.append(newGame ? entry(file, exists: false) : skip(file, .gameExists))
            case .save: entries.append(entry(file, exists: data.save != nil))
            case .rtc: entries.append(entry(file, exists: data.rtc != nil))
            case .state:
                let slot = file.slot ?? 0
                entries.append(stateOK.contains(slot) ? entry(file, exists: data.slots[slot]?.state != nil) : skip(file, .locked))
            case .stateThumbnail:
                let slot = file.slot ?? 0
                entries.append(stateOK.contains(slot) ? entry(file, exists: data.slots[slot]?.hasThumbnail == true) : skip(file, .noState))
            case .cheats: entries.append(entry(file, exists: !data.cheats.isEmpty))
            case .art: entries.append(entry(file, exists: hasArt))
            default: entries.append(skip(file, .unsupportedFormat))
            }
        }
        return entries
    }

    private func targetQuery(_ params: [String: String]) -> Result<ArchiveTarget?, FakeResponseBox> {
        switch (params["gameId"], params["systemId"]) {
        case (nil, nil):
            return params["folderId"] == nil ? .success(nil) : .failure(.init(.error(400, code: "invalid_request", message: "folderId needs systemId")))
        case (let gameId?, nil): return .success(.game(gameId: gameId))
        case (nil, let systemId?): return .success(.newGame(systemId: systemId, folderId: params["folderId"]))
        default: return .failure(.init(.error(400, code: "invalid_request", message: "Give either gameId or systemId")))
        }
    }

    func inspectArchive(stagingId: String, params: [String: String]) -> FakeResponse {
        guard lock.withLock({ stagedFiles[stagingId] }) != nil else { return .error(404, code: "not_found", message: "Staging file not found") }
        guard let archive = stagedArchive(stagingId) else {
            return .error(422, code: "unsupported_format", message: "Invalid game archive")
        }
        let given: ArchiveTarget?
        switch targetQuery(params) {
        case .success(let target): given = target
        case .failure(let box): return box.response
        }
        let target = given ?? suggest(archive.manifest)
        var plan = ArchivePlan(
            manifest: archive.manifest, target: nil, targetName: nil, suggested: given == nil,
            entries: archive.manifest.files.map { ArchivePlanEntry(role: $0.role, slot: $0.slot, sizeBytes: $0.sizeBytes, action: .skip, reason: .noTarget) },
            importBytes: 0
        )
        if let target {
            switch resolve(target, manifest: archive.manifest) {
            case .failure(let box):
                if given != nil { return box.response }
            case .success(let resolved):
                let entries = self.plan(archive.manifest, resolved)
                let name: String
                switch resolved {
                case .game(_, let game): name = game.name
                case .newGame(let system, _): name = system.label
                }
                plan = ArchivePlan(
                    manifest: archive.manifest, target: target, targetName: name, suggested: given == nil, entries: entries,
                    importBytes: entries.filter { $0.action != .skip }.reduce(0) { $0 + $1.sizeBytes }
                )
            }
        }
        guard let body = try? ProtocolJSON.encoder().encode(plan) else { return .error(500, code: "internal", message: "Internal error") }
        return FakeResponse(status: 200, headers: ["Content-Type": "application/json"], body: body)
    }

    func deleteStagedArchive(stagingId: String) -> FakeResponse {
        let removed = lock.withLock { stagedFiles.removeValue(forKey: stagingId) != nil }
        return removed ? FakeResponse(status: 204) : .error(404, code: "not_found", message: "Staging file not found")
    }

    func importArchive(stagingId: String, body: Data) -> FakeResponse {
        guard let request = try? ProtocolJSON.decoder().decode(ArchiveImportRequest.self, from: body) else {
            return .error(400, code: "invalid_request", message: "Missing target")
        }
        guard lock.withLock({ stagedFiles[stagingId] }) != nil else { return .error(404, code: "not_found", message: "Staging file not found") }
        guard let archive = stagedArchive(stagingId) else { return .error(422, code: "unsupported_format", message: "Invalid game archive") }
        if case .failure(let box) = resolve(request.target, manifest: archive.manifest) { return box.response }
        let now = Date()
        var job = FakeJob(
            id: Self.randomId("job_"), kind: JobKind.archiveImport.rawValue, state: "queued", done: 0, total: nil,
            createdAt: now, updatedAt: now, result: nil, error: nil, target: .staging(fileName: archive.manifest.source.gameName)
        )
        job.archive = FakeArchiveImport(stagingId: stagingId, manifest: archive.manifest, files: archive.files, target: request.target)
        lock.withLock { jobs[job.id] = job }
        emitJob(job)
        return .json(202, ["jobId": job.id])
    }

    /// One step of an `archive-import` job: queued → running → imported.
    func advanceArchiveImport(_ job: inout FakeJob) {
        guard let work = job.archive else { return }
        switch job.state {
        case "queued":
            job.state = "running"
            job.total = work.files.values.reduce(0) { $0 + Int64($1.count) }
        case "running":
            switch resolve(work.target, manifest: work.manifest) {
            case .failure(let box):
                job.state = "failed"
                let object = (try? JSONSerialization.jsonObject(with: box.response.body)) as? [String: Any]
                job.error = object?["error"] as? [String: Any] ?? ["code": "internal", "message": "Internal error"]
            case .success(let resolved):
                job.done = job.total ?? 0
                job.state = "succeeded"
                job.result = apply(work, resolved)
            }
        default:
            break
        }
    }

    private func apply(_ work: FakeArchiveImport, _ resolved: ResolvedTarget) -> [String: Any] {
        let entries = plan(work.manifest, resolved)
        let (system, game): (DemoLibrary.System, DemoLibrary.Game)
        switch resolved {
        case .game(let s, let g): (system, game) = (s, g)
        case .newGame(let s, let fileName):
            let rom = work.manifest.files.first { $0.role == .rom }
            let created = DemoLibrary.Game(path: fileName, sizeBytes: rom?.sizeBytes ?? 0, modifiedAt: Date())
            lock.withLock {
                if let index = _library.systems.firstIndex(where: { $0.id == s.id }) { _library.systems[index].games.append(created) }
            }
            (system, game) = (s, created)
        }
        let gameId = DemoLibrary.id(system.folder + "/" + game.path)
        _ = currentData(system: system, game: game)
        let key = Self.dataKey(system: system, game: game)
        var replaced: [String] = []
        var replacedBytes: Int64 = 0
        var backupId: Any = NSNull()
        var imported = 0
        var skipped: [[String: Any]] = []
        let thumbnails = Set(work.manifest.files.filter { $0.role == .stateThumbnail }.compactMap(\.slot))
        for (file, entry) in zip(work.manifest.files, entries) {
            guard entry.action != .skip, let bytes = work.files[file.path] else {
                var item: [String: Any] = ["role": file.role.rawValue, "reason": entry.reason?.rawValue ?? "unsupported-format"]
                if let slot = file.slot { item["slot"] = slot }
                skipped.append(item)
                continue
            }
            imported += 1
            if entry.action == .replace {
                replaced.append("\(file.role.rawValue)\(file.slot.map { " \($0)" } ?? "")")
                replacedBytes += file.sizeBytes
            }
            switch file.role {
            case .save:
                lock.withLock {
                    var data = gameData[key] ?? seed(game: game)
                    if let old = data.save { backupId = Self.addBackup(&data, blob: old, label: nil, reason: BackupReason.beforeImport.rawValue).id }
                    data.save = .of(bytes)
                    data.saveModifiedAt = Date()
                    gameData[key] = data
                }
            case .rtc:
                lock.withLock { gameData[key]?.rtc = .of(bytes) }
            case .state:
                let slot = file.slot ?? 0
                lock.withLock {
                    gameData[key]?.slots[slot] = FakeSlot(state: .of(bytes), modifiedAt: Date(), hasThumbnail: thumbnails.contains(slot), lockCopy: nil)
                }
            case .cheats:
                lock.withLock { gameData[key]?.cheats = Self.parseCHT(String(decoding: bytes, as: UTF8.self)) }
            case .art:
                lock.withLock {
                    customArt[gameId] = bytes
                    removedArt.remove(gameId)
                }
            default:
                break
            }
        }
        var trashId: Any = NSNull()
        if !replaced.isEmpty {
            let moved = lock.withLock { trash(reason: "import-archive", label: game.name, files: replaced, bytes: replacedBytes) }
            trashId = moved["trashEntryId"] ?? NSNull()
        }
        lock.withLock { _ = stagedFiles.removeValue(forKey: work.stagingId) }
        emitLibrary([system.id])
        return ["gameId": gameId, "imported": imported, "skipped": skipped, "trashEntryId": trashId, "saveBackupId": backupId]
    }
}

/// A response carried by a `Result` failure.
struct FakeResponseBox: Error {
    let response: FakeResponse
    init(_ response: FakeResponse) { self.response = response }
}
