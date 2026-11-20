import Foundation
import ReminisLinkKit

// Milestone M6 of the fake pak: `POST /games/{gameId}/patch` (a job of
// kind `patch`) and `GET /sd/files?type=patch`. It follows
// protocol/openapi.yaml without applying anything: the fake ROMs have no
// content, so a patch is judged by its header (`PATCH`, `BPS1`, `UPS1`)
// and, for BPS and UPS, by the source size it declares, compared with
// the size of the game (the real pak compares CRC32s).
//
// Conventions of the demo and the tests:
// - a staging file without bytes (a download, or an upload over 1 MiB) is
//   judged by its extension; a name containing "mismatch" does not match;
// - a file with bytes and no known header is refused at once (`422
//   unsupported_format`), as are multi-disc games and `.zip`/`.7z` ROMs;
// - a BPS or UPS whose declared target size is the game's size while its
//   source size is not is taken as already applied (`already-patched`);
// - an output name that does not end with `.<extension of the ROM>`
//   gets it (`Hack.zip` → `Hack.zip.gba`).

/// A file of the staging area.
struct FakeStagedFile {
    let name: String
    /// Kept for small uploads only.
    let bytes: Data?
}

/// What a patch job will do.
struct FakePatchPlan {
    enum Outcome: Equatable {
        case apply(targetSize: Int64)
        /// Another ROM: the source size the patch declares, if known.
        case mismatch(expectedSize: Int64?)
        case alreadyPatched
        case unsupported
        /// The ROM changed while the job ran (`409 conflict`).
        case romChanged
    }

    let gameId: String
    let outcome: Outcome
    let mode: String
    let fileName: String?
    /// `result.verified`: false for IPS (no checksum).
    var verified = true
}

/// Minimal patch files for the demo and the tests (never a real game's
/// patch): BPS made of one `TargetRead`, IPS records, UPS headers.
public enum DemoPatch {
    /// A valid BPS that writes `target` whatever the source, but declares
    /// the source by its size and CRC32 (which the pak checks).
    public static func bps(sourceSize: Int, sourceCRC32: UInt32, target: Data) -> Data {
        var out = Data("BPS1".utf8)
        out += varint(UInt64(sourceSize))
        out += varint(UInt64(target.count))
        out += varint(0)
        if !target.isEmpty {
            // TargetRead (command 1) of the whole target.
            out += varint(UInt64(target.count - 1) << 2 | 1)
            out += target
        }
        out += le32(sourceCRC32)
        out += le32(crc32(target))
        out += le32(crc32(out))
        return out
    }

    /// An IPS that writes `bytes` at `offset` (IPS checks no source).
    public static func ips(offset: Int, bytes: Data) -> Data {
        var out = Data("PATCH".utf8)
        out += Data([UInt8(offset >> 16 & 0xff), UInt8(offset >> 8 & 0xff), UInt8(offset & 0xff)])
        out += Data([UInt8(bytes.count >> 8 & 0xff), UInt8(bytes.count & 0xff)])
        out += bytes
        out += Data("EOF".utf8)
        return out
    }

    /// The header of a UPS (enough for the fake pak; not a complete patch).
    public static func upsHeader(sourceSize: Int, targetSize: Int) -> Data {
        Data("UPS1".utf8) + varint(UInt64(sourceSize)) + varint(UInt64(targetSize))
    }

    /// The variable-length numbers of BPS and UPS.
    public static func varint(_ value: UInt64) -> Data {
        var data = value
        var out = Data()
        while true {
            let x = UInt8(data & 0x7f)
            data >>= 7
            if data == 0 {
                out.append(0x80 | x)
                return out
            }
            out.append(x)
            data -= 1
        }
    }

    /// Reads a variable-length number at `index` (nil if truncated).
    static func readVarint(_ data: Data, at index: inout Data.Index) -> UInt64? {
        var value: UInt64 = 0
        var shift: UInt64 = 1
        while index < data.endIndex {
            let x = data[index]
            index += 1
            value += UInt64(x & 0x7f) &* shift
            if x & 0x80 != 0 { return value }
            shift <<= 7
            value &+= shift
            if shift > 1 << 56 { return nil }
        }
        return nil
    }

    static func le32(_ value: UInt32) -> Data {
        Data([UInt8(value & 0xff), UInt8(value >> 8 & 0xff), UInt8(value >> 16 & 0xff), UInt8(value >> 24 & 0xff)])
    }

    /// CRC-32 (IEEE 802.3), as zlib.
    public static func crc32(_ data: Data) -> UInt32 {
        var crc: UInt32 = 0xffff_ffff
        for byte in data {
            crc ^= UInt32(byte)
            for _ in 0..<8 {
                crc = crc & 1 != 0 ? (crc >> 1) ^ 0xedb8_8320 : crc >> 1
            }
        }
        return ~crc
    }
}

extension FakePak {
    /// The patches of the fake SD card: under `Roms/` and
    /// `ReminisLink/patches/`. Their bytes are headers only.
    func sdPatchFiles() -> [(id: String, path: String, bytes: Data)] {
        let files: [(String, Data)] = [
            ("Roms/Handheld Advance (GBA)/Sky Garden Quest (Translation).bps",
             DemoPatch.bps(sourceSize: 16_777_216, sourceCRC32: 0x1234_5678, target: Data("demo".utf8))),
            ("Roms/Handheld Advance (GBA)/Pixel Runner (Hack).ups",
             DemoPatch.upsHeader(sourceSize: 4_194_304, targetSize: 4_194_304)),
            ("ReminisLink/patches/Bonus Levels.ips", DemoPatch.ips(offset: 0x100, bytes: Data("demo".utf8))),
            ("ReminisLink/patches/Read Me.ips", Data("not a patch\n".utf8)),
        ]
        return files.map { ("sd_" + DemoLibrary.id($0.0), $0.0, $0.1) }
    }

    /// What a patch does to a game of `gameSize` bytes.
    static func judge(bytes: Data?, name: String, gameSize: Int64) -> FakePatchPlan.Outcome {
        guard let bytes else {
            let ext = name.split(separator: ".").last.map { $0.lowercased() } ?? ""
            guard ["ips", "bps", "ups"].contains(ext) else { return .unsupported }
            return name.lowercased().contains("mismatch") ? .mismatch(expectedSize: nil) : .apply(targetSize: gameSize)
        }
        if bytes.starts(with: Data("PATCH".utf8)) { return .apply(targetSize: gameSize) }
        let isBPS = bytes.starts(with: Data("BPS1".utf8))
        let isUPS = bytes.starts(with: Data("UPS1".utf8))
        guard isBPS || isUPS else { return .unsupported }
        var index = bytes.startIndex + 4
        guard let source = DemoPatch.readVarint(bytes, at: &index), let target = DemoPatch.readVarint(bytes, at: &index) else {
            return .unsupported
        }
        guard Int64(source) == gameSize else { return Int64(target) == gameSize ? .alreadyPatched : .mismatch(expectedSize: Int64(source)) }
        return .apply(targetSize: Int64(target))
    }

    func createPatchJob(gameId: String, body: Data) -> FakeResponse {
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let patch = object["patch"] as? [String: Any], let type = patch["type"] as? String
        else { return .error(400, code: "invalid_request", message: "Invalid patch request") }
        let output = object["output"] as? [String: Any] ?? [:]
        let mode = output["mode"] as? String ?? "new-file"
        guard mode == "new-file" || mode == "replace" else {
            return .error(400, code: "invalid_request", message: "Invalid output mode")
        }
        guard let (_, game) = library.game(id: gameId) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        var fileName: String?
        if mode == "new-file", let raw = output["fileName"] as? String {
            guard var clean = Self.cleanFileName(raw), !clean.hasPrefix(".") else {
                return .error(400, code: "invalid_name", message: "Invalid file name")
            }
            let romExt = game.fileName.split(separator: ".").count > 1 ? String(game.fileName.split(separator: ".").last ?? "") : ""
            if !romExt.isEmpty, !clean.lowercased().hasSuffix("." + romExt.lowercased()) {
                clean += "." + romExt
            }
            fileName = clean
        }
        let staged: (name: String, bytes: Data?)
        switch type {
        case "staging":
            guard let id = patch["stagingId"] as? String, let file = lock.withLock({ stagedFiles[id] }) else {
                return .error(404, code: "not_found", message: "Staging file not found")
            }
            staged = (file.name, file.bytes)
        case "sd":
            guard let id = patch["sdFileId"] as? String, let file = sdPatchFiles().first(where: { $0.id == id }) else {
                return .error(404, code: "not_found", message: "File not found")
            }
            staged = (file.path, file.bytes)
        default:
            return .error(400, code: "invalid_request", message: "Unknown patch type")
        }
        guard game.kind == .file else {
            return .error(422, code: "unsupported_format", message: "Multi-disc games cannot be patched")
        }
        let romExt = game.fileName.split(separator: ".").last.map { $0.lowercased() } ?? ""
        guard !["zip", "7z"].contains(romExt) else {
            return .error(422, code: "unsupported_format", message: "Archives cannot be patched")
        }
        var outcome = Self.judge(bytes: staged.bytes, name: staged.name, gameSize: game.sizeBytes)
        if outcome == .unsupported, staged.bytes != nil {
            return .error(422, code: "unsupported_format", message: "Not an IPS, BPS or UPS patch")
        }
        let changes = lock.withLock { () -> Bool in
            defer { _romChangesDuringNextPatch = false }
            return _romChangesDuringNextPatch
        }
        if changes, case .apply = outcome { outcome = .romChanged }
        let now = Date()
        let plan = FakePatchPlan(gameId: gameId, outcome: outcome, mode: mode, fileName: fileName,
                                 verified: staged.bytes.map { !$0.starts(with: Data("PATCH".utf8)) }
                                    ?? !staged.name.lowercased().hasSuffix(".ips"))
        let job = FakeJob(
            id: Self.randomId("job_"), kind: "patch", state: "queued", done: 0, total: nil,
            createdAt: now, updatedAt: now, result: nil, error: nil, target: .staging(fileName: nil),
            patch: plan
        )
        lock.withLock { jobs[job.id] = job }
        emitJob(job)
        return .json(202, ["jobId": job.id])
    }

    /// One step of a patch job: queued → running → a third per call →
    /// succeeded; a mismatch or an unknown format fails at the first
    /// running step (BPS and UPS check the source before writing).
    func advancePatch(_ job: inout FakeJob) {
        guard let plan = job.patch else { return }
        guard let (system, game) = library.game(id: plan.gameId) else {
            if job.state == "queued" || job.state == "running" {
                job.state = "failed"
                job.error = ["code": "not_found", "message": "Game not found", "details": [String: Any]()]
            }
            return
        }
        switch job.state {
        case "queued":
            job.state = "running"
            job.total = game.sizeBytes
        case "running":
            switch plan.outcome {
            case .mismatch(let expectedSize):
                job.state = "failed"
                // The sizes differ in the fake: no `actualCrc32`.
                var details: [String: Any] = ["reason": "source-mismatch", "actualSize": game.sizeBytes]
                if let expectedSize {
                    details["expectedSize"] = expectedSize
                    details["expectedCrc32"] = "1234abcd"
                }
                job.error = ["code": "patch_mismatch", "message": "The ROM does not match the source of the patch", "details": details]
            case .alreadyPatched:
                job.state = "failed"
                job.error = ["code": "patch_mismatch", "message": "The ROM is the target of the patch",
                             "details": ["reason": "already-patched"]]
            case .romChanged:
                job.state = "failed"
                job.error = ["code": "conflict", "message": "The ROM changed while it was patched", "details": [String: Any]()]
            case .unsupported:
                job.state = "failed"
                job.error = ["code": "unsupported_format", "message": "Not an IPS, BPS or UPS patch", "details": [String: Any]()]
            case .apply(let targetSize):
                let total = job.total ?? game.sizeBytes
                job.done = min(total, job.done + max(1, total / 3))
                guard job.done >= total else { return }
                job.state = "succeeded"
                job.result = ["gameId": finishPatch(plan, system: system, game: game, targetSize: targetSize), "verified": plan.verified]
            }
        default:
            break
        }
    }

    /// Writes the result: a new game next to the original (` (2)` on a
    /// conflict), or the original replaced (its ROM to the trash, its save,
    /// states and cheats kept). Returns the id of the produced game.
    private func finishPatch(_ plan: FakePatchPlan, system: DemoLibrary.System, game: DemoLibrary.Game, targetSize: Int64) -> String {
        if plan.mode == "replace" {
            let now = Date()
            let stamp = ProtocolJSON.formatDate(now).replacingOccurrences(of: "-", with: "").replacingOccurrences(of: ":", with: "")
            let entry: [String: Any] = [
                "id": "tr_\(stamp)_\(UUID().uuidString.prefix(4).lowercased())", "trashedAt": ProtocolJSON.formatDate(now),
                "reason": "replace-rom", "label": game.name, "files": ["Roms/\(system.folder)/\(game.path)"],
                "sizeBytes": game.sizeBytes, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
            ]
            let replaced = DemoLibrary.Game(
                path: game.path, kind: game.kind, sizeBytes: targetSize, modifiedAt: now, hasSave: game.hasSave,
                stateSlots: game.stateSlots, lockedStateCount: game.lockedStateCount, cheatCount: game.cheatCount,
                hasArt: game.hasArt
            )
            lock.withLock {
                trashEntries.insert(entry, at: 0)
                if let s = _library.systems.firstIndex(where: { $0.id == system.id }),
                   let g = _library.systems[s].games.firstIndex(where: { $0.path == game.path }) {
                    _library.systems[s].games[g] = replaced
                }
            }
            return plan.gameId
        }
        let base = plan.fileName ?? PatchNaming.suggestedFileName(forROM: game.fileName)
        let response = store(target: .rom(system: system, folder: game.parent, fileName: base), sizeBytes: targetSize, onConflict: "rename")
        let object = (try? JSONSerialization.jsonObject(with: response.body)) as? [String: Any]
        return object?["gameId"] as? String ?? plan.gameId
    }
}
extension FakePak {
    /// The next patch job finds its ROM changed (`409 conflict`).
    public var romChangesDuringNextPatch: Bool {
        get { lock.withLock { _romChangesDuringNextPatch } }
        set { lock.withLock { _romChangesDuringNextPatch = newValue } }
    }
}
