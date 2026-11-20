// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import Foundation
import ReminisLinkKit

// Milestone M12 of the fake pak: the tool and emulator paks (`GET /paks`,
// `DELETE /paks/{pakId}`, `POST /paks/install`, the `pak` targets of
// `/uploads` and `/downloads`, the `pak-install` jobs and the `paks`
// event) and the read-only SD browser (`GET /sd/browse`,
// `GET /sd/browse/{sdEntryId}/content`).
//
// The fake keeps no archive bytes: what an installation does is decided by
// the name of its source (the `fileName` of an upload, the file name of a
// download URL, the name of an SD entry), so the demo and the tests can
// reach every outcome:
// - a name containing `broken` is not a zip (`422 unsupported_format`,
//   `Invalid pak: not a zip archive`);
// - a name containing `nolaunch` has no `launch.sh` (`422`, `Invalid pak:
//   no launch.sh in the pak folder`);
// - a name containing `twopaks` holds two paks (`422`);
// - any other name installs the pak named after it (`.zip` and `.pak`
//   dropped: `Clock.pak.zip` installs `Clock`), `409 conflict` with
//   `details.existing` when a pak of that name exists (ignoring case) and
//   `onConflict` is not `replace`/`overwrite`, `409` with `details.self`
//   for ReminisLink itself.
// The SD card holds `Downloads/` with such archives, a `.pak` folder, the
// paks under `Tools/tg5040/` and `Emus/tg5040/`, the system folders of the
// library under `Roms/`, and `.userdata/shared/` without the ReminisLink
// folder (never listed, `404`).

/// An installed pak of the fake console.
struct FakeInstalledPak {
    var name: String
    var kind: String
    var sizeBytes: Int64
    var modifiedAt: Date
    var version: String?
    var isSelf = false

    var folder: String { (kind == "emu" ? "Emus" : "Tools") + "/tg5040/\(name).pak" }
    var pakId: String { "pk_" + Base64URL.encode(Data(folder.utf8)) }
}

/// What a `pak-install` job installs.
struct FakePakPlan {
    /// The name of the source, which decides the outcome.
    let sourceName: String
    let kind: String
    let onConflict: String
    /// The kind given by the SD folder of the source (auto only).
    var folderKind: String? = nil
}

/// The paks of the fake console and the state of its SD browser.
struct FakePaksState {
    var paks: [FakeInstalledPak] = FakePaksState.sample
    /// `/paks` and `/sd/browse` exist (false: a pak older than M12).
    var supported = true
    /// The next installation reads this name instead of its source's (the
    /// source changed between two attempts).
    var nextSourceName: String?

    static let sample: [FakeInstalledPak] = {
        let date = ProtocolJSON.parseDate("2026-11-18T17:00:00Z") ?? Date(timeIntervalSince1970: 1_795_000_000)
        return [
            FakeInstalledPak(name: "ReminisLink", kind: "tool", sizeBytes: 9_437_184, modifiedAt: date, version: "v1.0.0", isSelf: true),
            FakeInstalledPak(name: "Clock", kind: "tool", sizeBytes: 2048, modifiedAt: date, version: "1.2"),
            FakeInstalledPak(name: "Files", kind: "tool", sizeBytes: 1_258_291, modifiedAt: date, version: nil),
            FakeInstalledPak(name: "MGBA", kind: "emu", sizeBytes: 4096, modifiedAt: date, version: nil),
            FakeInstalledPak(name: "PCSX", kind: "emu", sizeBytes: 3_145_728, modifiedAt: date, version: "2.0"),
        ]
    }()
}

/// A node of the fake SD card.
private struct FakeSdNode {
    let name: String
    /// Nil for a folder.
    let size: Int64?
    let modifiedAt: Date
    var children: [FakeSdNode] = []

    var isFolder: Bool { size == nil }
}

extension FakePak {
    public static let maxPakArchiveBytes: Int64 = 1 << 30
    public static let maxSdEntries = 5000

    // MARK: - Controls

    /// Whether `/paks` and `/sd/browse` exist (false: a pak older than
    /// M12, `404`).
    public var paksSupported: Bool {
        get { lock.withLock { paksState.supported } }
        set { lock.withLock { paksState.supported = newValue } }
    }

    /// The names of the installed paks, tools first.
    public var installedPakNames: [String] {
        lock.withLock { paksState.paks.sorted(by: Self.pakOrder).map(\.name) }
    }

    /// The installed pak of this name (ignoring case), if any: its kind
    /// (`tool` or `emu`) and its version.
    public func installedPak(named name: String) -> (kind: String, version: String?)? {
        lock.withLock {
            paksState.paks.first { $0.name.lowercased() == name.lowercased() }.map { ($0.kind, $0.version) }
        }
    }

    /// The `sdEntryId` of a path of the SD card (`Downloads/Clock.pak.zip`),
    /// as `GET /sd/browse` lists it.
    public static func sdEntryId(_ path: String) -> String {
        "se_" + Base64URL.encode(Data(path.utf8))
    }

    /// The next installation finds another archive than its source named
    /// (a link or SD file replaced between a conflict and its replacement):
    /// it installs the pak `name` names.
    public func changeNextPakSource(to name: String) {
        lock.withLock { paksState.nextSourceName = name }
    }

    /// A trash entry made earlier (tests: an old replacement of a pak).
    public func addTrashEntry(reason: String, label: String, trashedAt: Date) {
        let stamp = ProtocolJSON.formatDate(trashedAt)
        lock.withLock {
            trashEntries.append([
                "id": "tr_old_\(UUID().uuidString.prefix(4).lowercased())", "trashedAt": stamp, "reason": reason,
                "label": label, "files": [String](), "sizeBytes": 1024,
                "expiresAt": ProtocolJSON.formatDate(trashedAt.addingTimeInterval(30 * 86_400)),
            ])
        }
    }

    /// As if another client had installed a pak (a `paks` event follows).
    public func installPakFromAnotherClient(name: String, kind: String = "tool") {
        lock.withLock {
            paksState.paks.removeAll { $0.name.lowercased() == name.lowercased() }
            paksState.paks.append(FakeInstalledPak(name: name, kind: kind, sizeBytes: 1024, modifiedAt: Date(), version: nil))
        }
        emitPaks()
    }

    // MARK: - Routes

    func routePaks(_ request: URLRequest, method: String, segments: [String], params: [String: String], body: Data) -> FakeResponse? {
        switch (method, segments) {
        case ("GET", ["paks"]):
            return guarded { listPaks() }
        case ("POST", ["paks", "install"]):
            return guarded { installFromSd(body: body) }
        case ("DELETE", let s) where s.count == 2 && s[0] == "paks":
            return guarded { deletePak(id: s[1]) }
        case ("GET", ["sd", "browse"]):
            return guarded { browse(folderId: params["folderId"]) }
        case ("GET", let s) where s.count == 4 && s[0] == "sd" && s[1] == "browse" && s[3] == "content":
            return guarded { sdContent(id: s[2]) }
        default:
            return nil
        }
    }

    private func guarded(_ body: () -> FakeResponse) -> FakeResponse {
        lock.withLock { paksState.supported } ? body() : .error(404, code: "not_found", message: "No route")
    }

    static func pakOrder(_ a: FakeInstalledPak, _ b: FakeInstalledPak) -> Bool {
        if a.kind != b.kind { return a.kind == "tool" }
        return a.name.lowercased() < b.name.lowercased()
    }

    private func listPaks() -> FakeResponse {
        let paks = lock.withLock { paksState.paks.sorted(by: Self.pakOrder) }
        return .json(200, ["paks": paks.map { pak -> [String: Any] in
            [
                "pakId": pak.pakId, "name": pak.name, "kind": pak.kind, "path": pak.folder,
                "sizeBytes": pak.sizeBytes, "modifiedAt": ProtocolJSON.formatDate(pak.modifiedAt),
                "version": pak.version.map { $0 as Any } ?? NSNull(), "self": pak.isSelf,
                "futureField": "ignored by the app",
            ]
        }])
    }

    private func deletePak(id: String) -> FakeResponse {
        let result = lock.withLock { () -> FakeResponse in
            guard let index = paksState.paks.firstIndex(where: { $0.pakId == id }) else {
                return .error(404, code: "not_found", message: "Pak not found")
            }
            let pak = paksState.paks[index]
            guard !pak.isSelf else {
                return .error(409, code: "conflict", message: "ReminisLink cannot delete or replace itself", details: ["self": true])
            }
            paksState.paks.remove(at: index)
            return .json(200, trashPak(pak, reason: "delete-pak"))
        }
        if result.status == 200 { emitPaks() }
        return result
    }

    /// Moves a pak to the trash (lock held).
    private func trashPak(_ pak: FakeInstalledPak, reason: String) -> [String: Any] {
        let now = Date()
        let stamp = ProtocolJSON.formatDate(now).replacingOccurrences(of: "-", with: "").replacingOccurrences(of: ":", with: "")
        let id = "tr_\(stamp)_\(UUID().uuidString.prefix(4).lowercased())"
        let files = ["\(pak.folder)/launch.sh", "\(pak.folder)/pak.json"]
        trashEntries.insert([
            "id": id, "trashedAt": ProtocolJSON.formatDate(now), "reason": reason, "label": pak.name,
            "files": files, "sizeBytes": pak.sizeBytes, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
        ], at: 0)
        return ["trashEntryId": id, "moved": files, "movedBytes": pak.sizeBytes]
    }

    private func installFromSd(body: Data) -> FakeResponse {
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let entryId = object["sdEntryId"] as? String
        else { return .error(400, code: "invalid_request", message: "Missing sdEntryId") }
        let kind = object["kind"] as? String ?? "auto"
        let onConflict = object["onConflict"] as? String ?? "fail"
        guard ["auto", "tool", "emu"].contains(kind), ["fail", "replace"].contains(onConflict) else {
            return .error(400, code: "invalid_request", message: "Invalid kind or onConflict")
        }
        guard let path = Self.sdPath(of: entryId) else { return .error(400, code: "invalid_id", message: "Invalid id") }
        guard let node = node(at: path) else { return .error(404, code: "not_found", message: "Entry not found") }
        let isPakFolder = node.isFolder && node.name.lowercased().hasSuffix(".pak")
        guard isPakFolder || !node.isFolder else {
            return .error(422, code: "unsupported_format", message: "Not a .pak folder nor an archive")
        }
        var plan = FakePakPlan(sourceName: node.name, kind: kind, onConflict: onConflict)
        if path.hasPrefix("Emus/") { plan.folderKind = "emu" }
        if path.hasPrefix("Tools/") { plan.folderKind = "tool" }
        let job = queuePakJob(plan)
        return .json(202, ["jobId": job.id])
    }

    /// Queues a `pak-install` job (`POST /paks/install`, or a completed
    /// `pak` upload).
    @discardableResult
    func queuePakJob(_ plan: FakePakPlan) -> FakeJob {
        let now = Date()
        var job = FakeJob(
            id: Self.randomId("job_"), kind: "pak-install", state: "queued", done: 0, total: nil,
            createdAt: now, updatedAt: now, result: nil, error: nil, target: .pak(kind: plan.kind, fileName: plan.sourceName),
            fails: false
        )
        job.pakPlan = plan
        lock.withLock { jobs[job.id] = job }
        emitJob(job)
        return job
    }

    /// One step of a `pak-install` job: queued → running → installed (or
    /// failed).
    func advancePakInstall(_ job: inout FakeJob) {
        guard let plan = job.pakPlan else { return }
        switch job.state {
        case "queued":
            job.state = "running"
            job.total = 4096
        case "running":
            let response = installPak(plan)
            job.done = 4096
            finish(&job, with: response)
        default:
            break
        }
    }

    /// The result or the error of a pak installation, into its job.
    func finish(_ job: inout FakeJob, with response: FakeResponse) {
        let object = (try? JSONSerialization.jsonObject(with: response.body)) as? [String: Any] ?? [:]
        if response.status == 200 {
            job.state = "succeeded"
            job.result = object
        } else {
            job.state = "failed"
            job.error = object["error"] as? [String: Any]
                ?? ["code": "internal", "message": "Store failed", "details": [String: Any]()]
        }
    }

    /// Installs the pak a plan names: `200 {pakId}` or the error of its job.
    func installPak(_ plan: FakePakPlan) -> FakeResponse {
        var plan = plan
        if let changed = lock.withLock({ () -> String? in
            defer { paksState.nextSourceName = nil }
            return paksState.nextSourceName
        }) {
            plan = FakePakPlan(sourceName: changed, kind: plan.kind, onConflict: plan.onConflict, folderKind: plan.folderKind)
        }
        let lower = plan.sourceName.lowercased()
        if lower.contains("broken") {
            return .error(422, code: "unsupported_format", message: "Invalid pak: not a zip archive")
        }
        if lower.contains("nolaunch") {
            return .error(422, code: "unsupported_format", message: "Invalid pak: no launch.sh in the pak folder")
        }
        if lower.contains("twopaks") {
            return .error(422, code: "unsupported_format", message: "Invalid pak: more than one pak in the archive")
        }
        var name = plan.sourceName
        if name.lowercased().hasSuffix(".zip") { name = String(name.dropLast(4)) }
        if name.lowercased().hasSuffix(".pak") { name = String(name.dropLast(4)) }
        guard let clean = Self.cleanFileName(name), !clean.isEmpty else {
            return .error(422, code: "unsupported_format",
                          message: "Invalid pak: cannot name the pak (no name in pak.json nor a usable archive name)")
        }
        let kind = plan.kind == "auto" ? (plan.folderKind ?? "tool") : plan.kind
        let replace = plan.onConflict == "replace" || plan.onConflict == "overwrite"
        let response = lock.withLock { () -> FakeResponse in
            if let index = paksState.paks.firstIndex(where: { $0.name.lowercased() == clean.lowercased() && $0.kind == kind }) {
                let existing = paksState.paks[index]
                if existing.isSelf {
                    return .error(409, code: "conflict", message: "ReminisLink cannot delete or replace itself", details: ["self": true])
                }
                guard replace else {
                    return .error(409, code: "conflict", message: "Pak exists", details: ["existing": existing.folder])
                }
                paksState.paks.remove(at: index)
                _ = trashPak(existing, reason: "replace-pak")
            }
            let pak = FakeInstalledPak(name: clean, kind: kind, sizeBytes: 4096, modifiedAt: Date(), version: "1.0")
            paksState.paks.append(pak)
            return .json(200, ["pakId": pak.pakId])
        }
        if response.status == 200 { emitPaks() }
        return response
    }

    /// `paks` (M12): an empty object.
    func emitPaks() {
        emit("paks", [String: Any]())
    }

    // MARK: - SD browser

    static func sdPath(of id: String) -> String? {
        guard id.hasPrefix("se_"), let data = Base64URL.decode(String(id.dropFirst(3))),
              let path = String(data: data, encoding: .utf8)
        else { return nil }
        let parts = path.split(separator: "/", omittingEmptySubsequences: false)
        guard !path.hasPrefix("/"), !parts.contains(where: { $0.isEmpty || $0 == ".." || $0 == "." }) else { return nil }
        return path
    }

    /// The SD card as it is now (paks and library included).
    private func sdTree() -> FakeSdNode {
        let date = ProtocolJSON.parseDate("2026-11-18T17:00:00Z") ?? Date()
        func folder(_ name: String, _ children: [FakeSdNode] = []) -> FakeSdNode {
            FakeSdNode(name: name, size: nil, modifiedAt: date, children: children)
        }
        func file(_ name: String, _ size: Int64) -> FakeSdNode {
            FakeSdNode(name: name, size: size, modifiedAt: date)
        }
        let paks = lock.withLock { paksState.paks }
        func pakFolders(_ kind: String) -> [FakeSdNode] {
            paks.filter { $0.kind == kind }.map { pak in
                FakeSdNode(name: pak.name + ".pak", size: nil, modifiedAt: pak.modifiedAt, children: [
                    file("launch.sh", 512), file("pak.json", 128),
                ])
            }
        }
        let systems = library.systems.map { system -> FakeSdNode in
            var children: [String: FakeSdNode] = [:]
            for game in system.games {
                let parts = game.path.split(separator: "/").map(String.init)
                if parts.count == 1 {
                    children[game.path] = FakeSdNode(name: game.path, size: game.sizeBytes, modifiedAt: game.modifiedAt)
                } else if children[parts[0]] == nil {
                    children[parts[0]] = folder(parts[0])
                }
            }
            return folder(system.folder, Array(children.values))
        }
        return folder("", [
            folder(".userdata", [
                // `ReminisLink/` (client keys, TLS key, trash, staging) is
                // never listed.
                folder("shared", [folder(".minui"), file("settings.txt", 64)]),
            ]),
            folder("Bios"),
            folder("Downloads", [
                file("Clock.pak.zip", 2048),
                file("Weather.pak.zip", 3072),
                file("broken.zip", 100),
                file("NoLaunch.zip", 1024),
                file("README.txt", 512),
                folder("Timer.pak", [file("launch.sh", 512), file("pak.json", 128)]),
            ]),
            folder("Emus", [folder("tg5040", pakFolders("emu"))]),
            folder("Roms", systems),
            folder("Saves"),
            folder("Tools", [folder("tg5040", pakFolders("tool"))]),
        ])
    }

    /// Whether a path names the data of ReminisLink (never listed).
    private static func isHidden(_ path: String) -> Bool {
        let lower = path.lowercased()
        return lower == ".userdata/shared/reminislink" || lower.hasPrefix(".userdata/shared/reminislink/")
    }

    private func node(at path: String) -> FakeSdNode? {
        guard !Self.isHidden(path) else { return nil }
        var node = sdTree()
        guard !path.isEmpty else { return node }
        for part in path.split(separator: "/") {
            guard let child = node.children.first(where: { $0.name == part }) else { return nil }
            node = child
        }
        return node
    }

    private func browse(folderId: String?) -> FakeResponse {
        var path = ""
        if let folderId {
            guard let decoded = Self.sdPath(of: folderId) else { return .error(400, code: "invalid_id", message: "Invalid id") }
            path = decoded
        }
        guard let node = node(at: path), node.isFolder else {
            return .error(404, code: "not_found", message: "Folder not found")
        }
        let sorted = node.children.sorted { a, b in
            if a.isFolder != b.isFolder { return a.isFolder }
            return a.name.lowercased() < b.name.lowercased()
        }
        let entries = sorted.prefix(Self.maxSdEntries).map { child -> [String: Any] in
            let childPath = path.isEmpty ? child.name : path + "/" + child.name
            return [
                "sdEntryId": Self.sdEntryId(childPath), "name": child.name, "kind": child.isFolder ? "folder" : "file",
                "sizeBytes": child.size.map { $0 as Any } ?? NSNull(), "modifiedAt": ProtocolJSON.formatDate(child.modifiedAt),
            ]
        }
        let parent = path.split(separator: "/").dropLast().joined(separator: "/")
        return .json(200, [
            "folderId": folderId.map { $0 as Any } ?? NSNull(), "path": path,
            "parentId": parent.isEmpty ? NSNull() : Self.sdEntryId(parent) as Any,
            "entries": Array(entries), "truncated": sorted.count > Self.maxSdEntries,
        ])
    }

    private func sdContent(id: String) -> FakeResponse {
        guard let path = Self.sdPath(of: id) else { return .error(400, code: "invalid_id", message: "Invalid id") }
        guard let node = node(at: path), let size = node.size else {
            return .error(404, code: "not_found", message: "File not found")
        }
        let content = Self.madeUpContent(size: size, tag: path)
        var response = fileResponse(name: node.name, content: content)
        response.headers["Content-Length"] = String(content.count)
        return response
    }
}
#endif
