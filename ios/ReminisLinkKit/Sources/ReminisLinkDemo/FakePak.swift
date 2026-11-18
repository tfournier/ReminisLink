// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import CryptoKit
import Foundation
import ReminisLinkKit

/// An in-memory pak that follows protocol/openapi.yaml for the routes the
/// app uses so far: `/hello`, `/pair`, signed `/device`, `/systems`,
/// `/systems/{id}/games`, `/games/{id}` (GET and DELETE), `/games/{id}/art`,
/// `/trash`, `DELETE /clients/me`, and the transfers of milestone M4:
/// `/uploads` (create, chunks with `Upload-Offset`, status, complete with
/// the SHA-256 check, cancel), `/downloads` and `/jobs`.
///
/// Uploads keep only a running SHA-256, never the bytes, so large files
/// cost no memory. Faults can be injected (lost connection before or after
/// a chunk is stored, restart of the pak, low free space).
///
/// It checks signatures like the pak (unknown client, clock skew, replayed
/// nonce, HMAC, body hash, duplicate query keys), so the app and the tests
/// exercise the real signing path. Its example values come from the
/// examples of openapi.yaml, with made-up game names.
public final class FakePak: FakeHTTPHandler, @unchecked Sendable {
    public static let demoHost = "demo.reminislink.invalid"
    public static let demoPort: UInt16 = 47821
    public static let demoDeviceId = "5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0"
    /// Fixed so the demo pairing link is stable (bytes 0x00 to 0x1f).
    public static let demoSecret = Data((0..<32).map { UInt8($0) })
    public static let demoPIN = "246810"
    /// `ETag` of every demo art.
    public static let artETag = "\"demo-art-1\""
    /// A 1x1 PNG.
    public static let artPNG = Data(base64Encoded: "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==")!

    public struct LoggedRequest: Sendable, Equatable {
        public let method: String
        public let path: String
        public let query: String
        public let nonce: String?
        public let status: Int
    }

    public let deviceId: String
    public let deviceName: String
    public let model: DeviceModel
    public let secret: Data
    public let pin: String
    public let host: String
    public let port: UInt16
    public var library: DemoLibrary { lock.withLock { _library } }
    public let minClientVersion: String
    public let apiVersion: Int

    private let lock = NSLock()
    private var _clockOffset: TimeInterval = 0
    private var _responseDelay: TimeInterval = 0
    private var _pairDelay: TimeInterval = 0
    private var clients: [String: SymmetricKey] = [:]
    private var clientNames: [String: String] = [:]
    /// Last accepted signed request per client (`connectedClients`).
    private var lastSeen: [String: Date] = [:]
    private var nonces = Set<String>()
    private var _log: [LoggedRequest] = []
    private var queued: [FakeResponse] = []
    private var trashEntries: [[String: Any]]
    private var _library: DemoLibrary
    private var uploads: [String: FakeUpload] = [:]
    private var jobs: [String: FakeJob] = [:]
    private var chunkFaults: [ChunkFault] = []
    private var _chunkSize: Int64 = 4 << 20
    private var _freeBytes: Int64 = 40_265_318_400
    private var _downloadSize: Int64 = 8 << 20
    private var _chunkLog: [ChunkRecord] = []

    public init(
        deviceId: String = FakePak.demoDeviceId,
        deviceName: String = "Demo Brick",
        model: DeviceModel = .brick,
        secret: Data = FakePak.demoSecret,
        pin: String = FakePak.demoPIN,
        host: String = FakePak.demoHost,
        port: UInt16 = FakePak.demoPort,
        library: DemoLibrary = .sample,
        minClientVersion: String = "1.0.0",
        apiVersion: Int = 1
    ) {
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.model = model
        self.secret = secret
        self.pin = pin
        self.host = host
        self.port = port
        self._library = library
        self.minClientVersion = minClientVersion
        self.apiVersion = apiVersion
        trashEntries = [[
            "id": "tr_20261118T170000Z_a1b2",
            "trashedAt": "2026-11-18T17:00:00Z",
            "reason": "delete-game",
            "label": "Old Prototype",
            "files": ["Roms/Game Boy Advance (GBA)/Old Prototype.gba", "Saves/GBA/Old Prototype.gba.sav"],
            "sizeBytes": 17_377_096,
            "expiresAt": "2026-12-18T17:00:00Z",
        ]]
    }

    /// Registers this pak for its host on ``FakeURLProtocol``.
    public func register() {
        FakeURLProtocol.register(self, host: host)
    }

    public func unregister() {
        FakeURLProtocol.unregister(host: host)
    }

    /// A session that reaches this pak (and nothing else on its host).
    public func makeSession() -> URLSession {
        register()
        return APIClient.makeSession(protocolClasses: [FakeURLProtocol.self])
    }

    public var endpoint: Endpoint { Endpoint(host: host, port: port) }

    /// The link of the QR code. The demo discovery provides the endpoint.
    public var pairingLink: PairingLink {
        PairingLink(deviceId: deviceId, deviceName: deviceName, secret: secret)
    }

    /// What a Bonjour browser would see.
    public var discoveredDevice: DiscoveredDevice {
        DiscoveredDevice(
            id: deviceId, name: deviceName, model: model, platform: "tg5040", os: "nextui",
            osVersion: "6.14.0", pakVersion: "1.0.0", apiVersion: apiVersion,
            service: BonjourService(name: deviceName), fixedEndpoint: endpoint
        )
    }

    /// Device time minus real time (to test the clock skew correction).
    public var clockOffset: TimeInterval {
        get { lock.withLock { _clockOffset } }
        set { lock.withLock { _clockOffset = newValue } }
    }

    /// Every answer waits this long (to test cancellation).
    public var responseDelay: TimeInterval {
        get { lock.withLock { _responseDelay } }
        set { lock.withLock { _responseDelay = newValue } }
    }

    /// `POST /pair` waits this long after registering the client, as if
    /// the answer were slow (to test a cancellation during `/pair`).
    public var pairDelay: TimeInterval {
        get { lock.withLock { _pairDelay } }
        set { lock.withLock { _pairDelay = newValue } }
    }

    public var log: [LoggedRequest] { lock.withLock { _log } }

    // MARK: - Transfer controls

    /// What happens to a chunk request.
    public enum ChunkFault: Sendable, Equatable {
        /// The connection drops before the chunk is stored.
        case dropBeforeStoring
        /// The chunk is stored, then the answer is lost.
        case dropAfterStoring
        /// `503 shutting_down`.
        case shuttingDown
    }

    /// A chunk as received.
    public struct ChunkRecord: Sendable, Equatable {
        public let uploadId: String
        public let offset: Int64
        public let length: Int
        /// `X-RL-Content-SHA256` of the request (must be `UNSIGNED`).
        public let contentSHA256: String
        public let nonce: String
    }

    /// `chunkSize` returned by `POST /uploads`.
    public var chunkSize: Int64 {
        get { lock.withLock { _chunkSize } }
        set { lock.withLock { _chunkSize = newValue } }
    }

    /// Free space of the SD card (checked at upload creation: size + 5 %).
    public var freeBytes: Int64 {
        get { lock.withLock { _freeBytes } }
        set { lock.withLock { _freeBytes = newValue } }
    }

    /// Size of every simulated download.
    public var downloadSize: Int64 {
        get { lock.withLock { _downloadSize } }
        set { lock.withLock { _downloadSize = newValue } }
    }

    public var chunkLog: [ChunkRecord] { lock.withLock { _chunkLog } }

    public var uploadIds: [String] { lock.withLock { uploads.keys.sorted() } }

    /// The next chunk requests meet these faults, in order.
    public func injectChunkFaults(_ faults: [ChunkFault]) {
        lock.withLock { chunkFaults.append(contentsOf: faults) }
    }

    /// As if the pak were closed and opened again: uploads in progress,
    /// jobs, the nonce cache and the connected clients are lost.
    public func restart() {
        lock.withLock {
            uploads = [:]
            jobs = [:]
            nonces = []
            lastSeen = [:]
        }
    }

    public var clientIds: [String] { lock.withLock { clients.keys.sorted() } }

    /// Registers a client directly, as if it had paired.
    public func addClient(id: String, key: SymmetricKey, name: String = "Test iPhone") {
        lock.withLock {
            clients[id] = key
            clientNames[id] = name
        }
    }

    /// The next requests get these responses instead (after the signature
    /// check for signed routes).
    public func enqueue(_ response: FakeResponse) {
        lock.withLock { queued.append(response) }
    }

    // MARK: - Handling

    public func respond(to request: URLRequest, body: Data) -> FakeResponse {
        if responseDelay > 0 { Thread.sleep(forTimeInterval: responseDelay) }
        let method = (request.httpMethod ?? "GET").uppercased()
        let url = request.url!
        let path = url.path(percentEncoded: true)
        let query = url.query(percentEncoded: true) ?? ""
        let response = route(request, method: method, path: path, query: query, body: body)
        lock.withLock {
            _log.append(LoggedRequest(
                method: method, path: path, query: query,
                nonce: request.value(forHTTPHeaderField: SignedHeaders.nonce), status: response.status
            ))
        }
        return response
    }

    private func route(_ request: URLRequest, method: String, path: String, query: String, body: Data) -> FakeResponse {
        let prefix = APIClient.apiPrefix + "/"
        guard path.hasPrefix(prefix) else { return .error(404, code: "not_found", message: "No route") }
        let segments = path.dropFirst(prefix.count).split(separator: "/", omittingEmptySubsequences: false)
            .map { $0.removingPercentEncoding ?? String($0) }

        switch (method, segments) {
        case ("GET", ["hello"]):
            return hello()
        case ("POST", ["pair"]):
            return pair(body: body)
        default:
            break
        }

        if let failure = verify(request, method: method, path: path, query: query, body: body) {
            return failure
        }
        if let next = lock.withLock({ queued.isEmpty ? nil : queued.removeFirst() }) {
            return next
        }
        let params = Dictionary(uniqueKeysWithValues: Self.decodeQuery(query))

        switch (method, segments) {
        case ("GET", ["device"]):
            return device()
        case ("DELETE", ["clients", "me"]):
            let clientId = request.value(forHTTPHeaderField: SignedHeaders.client) ?? ""
            lock.withLock {
                _ = clients.removeValue(forKey: clientId)
                lastSeen[clientId] = nil
            }
            return FakeResponse(status: 204)
        case ("GET", ["systems"]):
            return systems()
        case ("GET", let s) where s.count == 3 && s[0] == "systems" && s[2] == "games":
            return games(systemId: s[1], folderId: params["folderId"])
        case ("GET", let s) where s.count == 2 && s[0] == "games":
            return game(id: s[1])
        case ("DELETE", let s) where s.count == 2 && s[0] == "games":
            return deleteGame(id: s[1], keepSave: params["keepSave"] == "true")
        case ("POST", ["uploads"]):
            return createUpload(body: body)
        case ("GET", let s) where s.count == 2 && s[0] == "uploads":
            return uploadStatus(id: s[1])
        case ("PUT", let s) where s.count == 2 && s[0] == "uploads":
            return uploadChunk(id: s[1], request: request, body: body)
        case ("DELETE", let s) where s.count == 2 && s[0] == "uploads":
            let removed = lock.withLock { uploads.removeValue(forKey: s[1]) != nil }
            return removed ? FakeResponse(status: 204) : .error(404, code: "not_found", message: "Upload not found")
        case ("POST", let s) where s.count == 3 && s[0] == "uploads" && s[2] == "complete":
            return completeUpload(id: s[1])
        case ("POST", ["downloads"]):
            return createDownload(body: body)
        case ("GET", ["jobs"]):
            let list = lock.withLock { jobs.values.sorted { $0.createdAt < $1.createdAt } }.map { advance($0.id) }
            return .json(200, ["jobs": list.compactMap { $0 }])
        case ("GET", let s) where s.count == 2 && s[0] == "jobs":
            guard let job = advance(s[1]) else { return .error(404, code: "not_found", message: "Job not found") }
            return .json(200, job)
        case ("DELETE", let s) where s.count == 2 && s[0] == "jobs":
            guard let job = cancelJob(s[1]) else { return .error(404, code: "not_found", message: "Job not found") }
            return .json(200, job)
        case ("GET", let s) where s.count == 3 && s[0] == "games" && s[2] == "art":
            return art(gameId: s[1], ifNoneMatch: request.value(forHTTPHeaderField: "If-None-Match"))
        case ("GET", ["trash"]):
            return trash()
        case ("DELETE", ["trash"]):
            guard request.value(forHTTPHeaderField: "X-RL-Confirm") == "empty-trash" else {
                return .error(400, code: "invalid_request", message: "Missing confirmation")
            }
            let freed = lock.withLock { () -> Int64 in
                let total = trashEntries.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
                trashEntries = []
                _freeBytes += total
                return total
            }
            return .json(200, ["freedBytes": freed])
        case ("DELETE", let s) where s.count == 2 && s[0] == "trash":
            guard request.value(forHTTPHeaderField: "X-RL-Confirm") == "empty-trash" else {
                return .error(400, code: "invalid_request", message: "Missing confirmation")
            }
            let freed = lock.withLock { () -> Int64? in
                guard let index = trashEntries.firstIndex(where: { $0["id"] as? String == s[1] }) else { return nil }
                let size = Self.size(trashEntries.remove(at: index)["sizeBytes"])
                _freeBytes += size
                return size
            }
            guard let freed else { return .error(404, code: "not_found", message: "Trash entry not found") }
            return .json(200, ["freedBytes": freed])
        default:
            return .error(404, code: "not_found", message: "No route")
        }
    }

    /// The pak's checks, in the order of the contract.
    private func verify(_ request: URLRequest, method: String, path: String, query: String, body: Data) -> FakeResponse? {
        func header(_ name: String) -> String? { request.value(forHTTPHeaderField: name) }
        guard let clientId = header(SignedHeaders.client), let key = lock.withLock({ clients[clientId] }) else {
            return .error(401, code: "unknown_client", message: "Unknown client")
        }
        guard let time = header(SignedHeaders.time), let nonce = header(SignedHeaders.nonce),
              let content = header(SignedHeaders.contentSHA256), let signature = header(SignedHeaders.signature),
              let seconds = Int64(time)
        else { return .error(401, code: "bad_signature", message: "Malformed signature headers") }
        let deviceNow = Date().timeIntervalSince1970 + clockOffset
        guard abs(Double(seconds) - deviceNow) <= 300 else {
            return .error(401, code: "clock_skew", message: "Request time is too far from the device time")
        }
        if lock.withLock({ nonces.contains(clientId + "|" + nonce) }) {
            return .error(401, code: "replayed_request", message: "Replayed request")
        }
        guard let canonical = try? Signer.canonicalRequest(
            method: method, path: path, rawQuery: query, time: time, nonce: nonce, contentSHA256: content
        ), Signer.isValidSignature(signature, key: key, canonical: canonical)
        else { return .error(401, code: "bad_signature", message: "Bad signature") }
        let seen = now()
        lock.withLock {
            _ = nonces.insert(clientId + "|" + nonce)
            lastSeen[clientId] = seen
        }
        let keys = Self.decodeQuery(query).map(\.0)
        guard Set(keys).count == keys.count else {
            return .error(400, code: "invalid_request", message: "Duplicate query key")
        }
        if content == Signer.unsignedPayload {
            let segments = path.split(separator: "/")
            guard method == "PUT", segments.count == 4, segments[2] == "uploads" else {
                return .error(401, code: "bad_signature", message: "UNSIGNED is only allowed for upload chunks")
            }
            return nil
        }
        guard content == Signer.contentSHA256(body) else {
            return .error(401, code: "bad_signature", message: "Body hash mismatch")
        }
        return nil
    }

    // MARK: - Routes

    private func now() -> Date { Date().addingTimeInterval(clockOffset) }

    private func hello() -> FakeResponse {
        .json(200, [
            "deviceId": deviceId, "deviceName": deviceName, "model": model.rawValue,
            "platform": "tg5040", "os": "nextui", "osVersion": "6.14.0", "pakVersion": "1.0.0",
            "apiVersion": apiVersion, "minClientVersion": minClientVersion,
            "serverTime": ProtocolJSON.formatDate(now()), "pairingOpen": true,
        ])
    }

    private func pair(body: Data) -> FakeResponse {
        struct Request: Decodable {
            let method: String
            let clientId: String
            let clientName: String
            let clientNonce: String
            let proof: String
        }
        guard let request = try? JSONDecoder().decode(Request.self, from: body),
              request.clientId.range(of: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", options: .regularExpression) != nil,
              (1...64).contains(request.clientName.count),
              let clientNonce = Base64URL.decode(request.clientNonce), clientNonce.count == 32
        else { return .error(400, code: "invalid_request", message: "Invalid pairing request") }
        let ikm: Data
        switch request.method {
        case "qr": ikm = secret
        case "pin": ikm = Data(pin.utf8)
        default: return .error(400, code: "invalid_request", message: "Invalid method")
        }
        let expected = Pairing.clientProof(ikm: ikm, deviceId: deviceId, clientId: request.clientId, clientNonce: request.clientNonce)
        guard expected == request.proof else { return .error(401, code: "pairing_failed", message: "Pairing failed") }
        let deviceNonce = Pairing.makeNonce()
        guard let key = try? Pairing.deriveKey(
            ikm: ikm, clientNonce: clientNonce, deviceNonce: deviceNonce, deviceId: deviceId, clientId: request.clientId
        ) else { return .error(500, code: "internal", message: "Key derivation failed") }
        addClient(id: request.clientId, key: key, name: request.clientName)
        if pairDelay > 0 { Thread.sleep(forTimeInterval: pairDelay) }
        return .json(200, [
            "deviceId": deviceId, "deviceName": deviceName,
            "deviceNonce": Base64URL.encode(deviceNonce),
            "proof": Pairing.deviceProof(key: key, deviceId: deviceId, clientId: request.clientId),
        ])
    }

    private func device() -> FakeResponse {
        let limit = now().addingTimeInterval(-30)
        let connected = lock.withLock { () -> [[String: Any]] in
            return lastSeen.filter { $0.value >= limit && clients[$0.key] != nil }
                .map { (name: clientNames[$0.key] ?? "", seen: $0.value) }
                .sorted { $0.name < $1.name }
                .map { ["clientName": $0.name, "lastSeenAt": ProtocolJSON.formatDate($0.seen)] }
        }
        return .json(200, [
            "deviceId": deviceId, "deviceName": deviceName, "model": model.rawValue,
            "platform": "tg5040", "os": "nextui", "osVersion": "6.14.0", "pakVersion": "1.0.0",
            "network": ["ipv4": "192.168.1.42", "ssid": "DemoNet"],
            "battery": ["percent": 78, "charging": false],
            "storage": [["id": "sd", "label": "SD", "totalBytes": 127_865_454_592, "freeBytes": freeBytes]],
            "uptimeSeconds": 512,
            "connectedClients": connected,
        ])
    }

    private func systems() -> FakeResponse {
        let list = library.systems
            .sorted { $0.label.localizedCaseInsensitiveCompare($1.label) == .orderedAscending }
            .map { s -> [String: Any] in
                ["id": s.id, "label": s.label, "folder": s.folder, "tag": s.tag, "gameCount": s.games.count]
            }
        return .json(200, ["systems": list])
    }

    private func games(systemId: String, folderId: String?) -> FakeResponse {
        guard let system = library.system(id: systemId) else {
            return .error(404, code: "not_found", message: "System not found")
        }
        guard let folder = library.folderPath(system: system, folderId: folderId) else {
            return .error(404, code: "not_found", message: "Folder not found")
        }
        let prefix = folder.isEmpty ? "" : folder + "/"
        var subfolders: [String: Int] = [:]
        var games: [[String: Any]] = []
        for game in system.games where game.path.hasPrefix(prefix) {
            let rest = game.path.dropFirst(prefix.count)
            if let slash = rest.firstIndex(of: "/") {
                subfolders[String(rest[..<slash]), default: 0] += 1
            } else {
                games.append(summary(system: system, game: game))
            }
        }
        let folders = subfolders.keys.sorted().map { name -> [String: Any] in
            ["id": DemoLibrary.id(system.folder + "/" + prefix + name), "name": name, "gameCount": subfolders[name]!]
        }
        return .json(200, ["folders": folders, "games": games])
    }

    private func summary(system: DemoLibrary.System, game: DemoLibrary.Game) -> [String: Any] {
        [
            "id": DemoLibrary.id(system.folder + "/" + game.path), "name": game.name, "fileName": game.fileName,
            "kind": game.kind.rawValue, "sizeBytes": game.sizeBytes,
            "modifiedAt": ProtocolJSON.formatDate(game.modifiedAt), "hasSave": game.hasSave,
            "stateCount": game.stateSlots.count, "lockedStateCount": game.lockedStateCount,
            "cheatCount": game.cheatCount, "hasArt": game.hasArt,
            "futureField": "ignored by the app",
        ]
    }

    private func game(id: String) -> FakeResponse {
        guard let (system, game) = library.game(id: id) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        let files = Self.files(system: system, game: game)
        let total = files.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
        return .json(200, [
            "id": id, "systemId": system.id, "name": game.name, "fileName": game.fileName,
            "kind": game.kind.rawValue, "files": files, "totalBytes": total,
        ])
    }

    /// The files of a game, as `GET /games/{id}` lists them.
    static func files(system: DemoLibrary.System, game: DemoLibrary.Game) -> [[String: Any]] {
        let date = ProtocolJSON.formatDate(game.modifiedAt)
        var files: [[String: Any]] = [
            ["role": "rom", "path": "Roms/\(system.folder)/\(game.path)", "sizeBytes": game.sizeBytes, "modifiedAt": date],
        ]
        if game.hasSave {
            files.append(["role": "save", "path": "Saves/\(system.tag)/\(game.fileName).sav", "sizeBytes": 131_072, "modifiedAt": date])
        }
        for slot in game.stateSlots {
            files.append([
                "role": "state", "slot": slot, "path": ".userdata/shared/.minui/\(system.tag)/\(game.fileName).st\(slot)",
                "sizeBytes": 401_408, "modifiedAt": date,
            ])
        }
        if game.cheatCount > 0 {
            files.append(["role": "cheats", "path": "Cheats/\(system.tag)/\(game.fileName).cht", "sizeBytes": 2048, "modifiedAt": date])
        }
        if game.hasArt {
            files.append(["role": "art", "path": "Roms/\(system.folder)/.media/\(game.name).png", "sizeBytes": Int64(Self.artPNG.count), "modifiedAt": date])
        }
        return files
    }

    private func art(gameId: String, ifNoneMatch: String?) -> FakeResponse {
        guard let (_, game) = library.game(id: gameId), game.hasArt else {
            return .error(404, code: "not_found", message: "No art")
        }
        if ifNoneMatch == Self.artETag {
            return FakeResponse(status: 304, headers: ["ETag": Self.artETag])
        }
        return FakeResponse(status: 200, headers: [
            "Content-Type": "image/png", "ETag": Self.artETag, "Cache-Control": "private, max-age=86400",
        ], body: Self.artPNG)
    }

    private func trash() -> FakeResponse {
        let entries = lock.withLock { trashEntries }
        let total = entries.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
        return .json(200, ["entries": entries, "totalBytes": total])
    }

    private static func size(_ value: Any?) -> Int64 {
        switch value {
        case let v as Int64: v
        case let v as Int: Int64(v)
        default: 0
        }
    }

    /// Form-urlencoded pairs ("+" is a space).
    static func decodeQuery(_ raw: String) -> [(String, String)] {
        raw.split(separator: "&").map { part in
            let pieces = part.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
            func decode(_ s: Substring) -> String {
                s.replacingOccurrences(of: "+", with: " ").removingPercentEncoding ?? String(s)
            }
            return (decode(pieces[0]), pieces.count > 1 ? decode(pieces[1]) : "")
        }
    }
}
// MARK: - Transfers (milestone M4)

/// An upload in progress: only its running SHA-256 is kept.
private struct FakeUpload {
    var target: FakePak.Target
    let sizeBytes: Int64
    let sha256: String
    let onConflict: String
    var offset: Int64 = 0
    var hasher = SHA256()
}

private struct FakeJob {
    let id: String
    let kind: String
    var state: String
    var done: Int64
    var total: Int64?
    let createdAt: Date
    var updatedAt: Date
    var result: [String: Any]?
    var error: [String: Any]?
    let target: FakePak.Target
    let fails: Bool

    var json: [String: Any] {
        [
            "id": id, "kind": kind, "state": state,
            "progress": ["done": done, "total": total.map { $0 as Any } ?? NSNull()],
            "createdAt": ProtocolJSON.formatDate(createdAt), "updatedAt": ProtocolJSON.formatDate(updatedAt),
            "result": result.map { $0 as Any } ?? NSNull(), "error": error.map { $0 as Any } ?? NSNull(),
        ]
    }
}

extension FakePak {
    /// A parsed `target`.
    fileprivate enum Target {
        case rom(system: DemoLibrary.System, folder: String, fileName: String?)
        case save(gameId: String)
        case state(gameId: String, slot: Int)
        case staging(fileName: String?)
    }

    fileprivate enum ParsedTarget {
        case ok(Target)
        case refused(FakeResponse)
    }

    fileprivate static func randomId(_ prefix: String) -> String {
        prefix + UUID().uuidString.replacingOccurrences(of: "-", with: "").prefix(12).uppercased()
    }

    /// The file name rules of docs/specs/03-protocole.md §5 (nil if refused).
    static func cleanFileName(_ name: String) -> String? {
        let forbidden = Set("\"*:<>?|")
        guard !name.isEmpty, name != ".", name != "..", !name.contains("/"), !name.contains("\\"),
              !name.unicodeScalars.contains(where: { $0.value < 0x20 || $0.value == 0x7f }),
              name.utf8.count <= 255
        else { return nil }
        return String(name.map { forbidden.contains($0) ? "_" : $0 })
    }

    fileprivate func parseTarget(_ object: Any?, fileNameRequired: Bool) -> ParsedTarget {
        guard let t = object as? [String: Any], let kind = t["kind"] as? String else {
            return .refused(.error(400, code: "invalid_request", message: "Missing target"))
        }
        var fileName: String?
        if let raw = t["fileName"] as? String {
            guard let clean = Self.cleanFileName(raw) else {
                return .refused(.error(400, code: "invalid_name", message: "Invalid file name"))
            }
            fileName = clean
        }
        switch kind {
        case "rom":
            guard let systemId = t["systemId"] as? String else {
                return .refused(.error(400, code: "invalid_request", message: "Missing systemId"))
            }
            guard let system = library.system(id: systemId) else {
                return .refused(.error(404, code: "not_found", message: "System not found"))
            }
            guard let folder = library.folderPath(system: system, folderId: t["folderId"] as? String) else {
                return .refused(.error(404, code: "not_found", message: "Folder not found"))
            }
            if fileNameRequired && fileName == nil {
                return .refused(.error(400, code: "invalid_request", message: "Missing fileName"))
            }
            return .ok(.rom(system: system, folder: folder, fileName: fileName))
        case "save", "state":
            guard let gameId = t["gameId"] as? String else {
                return .refused(.error(400, code: "invalid_request", message: "Missing gameId"))
            }
            guard library.game(id: gameId) != nil else {
                return .refused(.error(404, code: "not_found", message: "Game not found"))
            }
            if kind == "save" { return .ok(.save(gameId: gameId)) }
            guard let slot = t["slot"] as? Int, slot >= 0 else {
                return .refused(.error(400, code: "invalid_request", message: "Missing slot"))
            }
            return .ok(.state(gameId: gameId, slot: slot))
        case "staging":
            if fileNameRequired && fileName == nil {
                return .refused(.error(400, code: "invalid_request", message: "Missing fileName"))
            }
            return .ok(.staging(fileName: fileName))
        default:
            return .refused(.error(400, code: "invalid_request", message: "Unknown target kind"))
        }
    }

    fileprivate func gamePath(_ system: DemoLibrary.System, folder: String, name: String) -> String {
        folder.isEmpty ? name : folder + "/" + name
    }

    func createUpload(body: Data) -> FakeResponse {
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let size = (object["sizeBytes"] as? NSNumber)?.int64Value, size >= 0,
              let sha = object["sha256"] as? String, sha.range(of: "^[0-9a-f]{64}$", options: .regularExpression) != nil
        else { return .error(400, code: "invalid_request", message: "Invalid upload request") }
        let onConflict = object["onConflict"] as? String ?? "fail"
        guard ["fail", "rename", "overwrite"].contains(onConflict) else {
            return .error(400, code: "invalid_request", message: "Invalid onConflict")
        }
        let target: Target
        switch parseTarget(object["target"], fileNameRequired: true) {
        case .ok(let t): target = t
        case .refused(let response): return response
        }
        if case .rom(let system, let folder, let name?) = target, onConflict == "fail" {
            let path = gamePath(system, folder: folder, name: name)
            if system.games.contains(where: { $0.path.lowercased() == path.lowercased() }) {
                return .error(409, code: "conflict", message: "Target exists",
                              details: ["existing": "Roms/\(system.folder)/\(path)"])
            }
        }
        let required = size + size / 20
        let free = freeBytes
        guard required <= free else {
            return .error(507, code: "insufficient_storage", message: "Not enough free space",
                          details: ["required": required, "available": free])
        }
        let id = Self.randomId("up_")
        let chunk = chunkSize
        lock.withLock {
            uploads[id] = FakeUpload(target: target, sizeBytes: size, sha256: sha, onConflict: onConflict)
        }
        return .json(201, [
            "uploadId": id, "chunkSize": chunk, "offset": 0,
            "expiresAt": ProtocolJSON.formatDate(Date().addingTimeInterval(86_400)),
        ])
    }

    func uploadStatus(id: String) -> FakeResponse {
        guard let upload = lock.withLock({ uploads[id] }) else {
            return .error(404, code: "not_found", message: "Upload not found")
        }
        return .json(200, ["uploadId": id, "offset": upload.offset, "sizeBytes": upload.sizeBytes])
    }

    func uploadChunk(id: String, request: URLRequest, body: Data) -> FakeResponse {
        let fault = lock.withLock { chunkFaults.isEmpty ? nil : chunkFaults.removeFirst() }
        if fault == .dropBeforeStoring { return .networkFailure(.networkConnectionLost) }
        if fault == .shuttingDown { return .error(503, code: "shutting_down", message: "Shutting down") }
        guard let header = request.value(forHTTPHeaderField: "Upload-Offset"), let offset = Int64(header) else {
            return .error(400, code: "invalid_request", message: "Missing Upload-Offset")
        }
        let chunk = chunkSize
        let result = lock.withLock { () -> FakeResponse in
            guard var upload = uploads[id] else { return .error(404, code: "not_found", message: "Upload not found") }
            guard offset == upload.offset else {
                return .error(409, code: "conflict", message: "Unexpected upload offset", details: ["offset": upload.offset])
            }
            guard !body.isEmpty, Int64(body.count) <= min(chunk, 16 << 20) else {
                return .error(413, code: "too_large", message: "Chunk too large")
            }
            guard offset + Int64(body.count) <= upload.sizeBytes else {
                return .error(400, code: "invalid_request", message: "Chunk beyond the announced size")
            }
            upload.hasher.update(data: body)
            upload.offset += Int64(body.count)
            uploads[id] = upload
            _chunkLog.append(ChunkRecord(
                uploadId: id, offset: offset, length: body.count,
                contentSHA256: request.value(forHTTPHeaderField: SignedHeaders.contentSHA256) ?? "",
                nonce: request.value(forHTTPHeaderField: SignedHeaders.nonce) ?? ""
            ))
            return FakeResponse(status: 204, headers: ["Upload-Offset": String(upload.offset)])
        }
        if fault == .dropAfterStoring && result.status == 204 { return .networkFailure(.networkConnectionLost) }
        return result
    }

    func completeUpload(id: String) -> FakeResponse {
        guard let upload = lock.withLock({ uploads[id] }) else {
            return .error(404, code: "not_found", message: "Upload not found")
        }
        guard upload.offset == upload.sizeBytes else {
            return .error(400, code: "invalid_request", message: "Upload incomplete")
        }
        let digest = upload.hasher.finalize().map { String(format: "%02x", $0) }.joined()
        guard digest == upload.sha256 else {
            lock.withLock { _ = uploads.removeValue(forKey: id) }
            return .error(409, code: "checksum_mismatch", message: "SHA-256 mismatch")
        }
        let response = store(target: upload.target, sizeBytes: upload.sizeBytes, onConflict: upload.onConflict)
        if response.status == 200 {
            lock.withLock {
                _ = uploads.removeValue(forKey: id)
                _freeBytes -= upload.sizeBytes
            }
        }
        return response
    }

    /// Writes a received file to its destination (upload or download).
    fileprivate func store(target: Target, sizeBytes: Int64, onConflict: String, fallbackName: String = "download.bin") -> FakeResponse {
        switch target {
        case .rom(let system, let folder, let fileName):
            let base = fileName ?? fallbackName
            var name = base
            let exists = { (n: String) in
                system.games.contains { $0.path.lowercased() == self.gamePath(system, folder: folder, name: n).lowercased() }
            }
            if exists(name) {
                switch onConflict {
                case "rename":
                    let dot = base.lastIndex(of: ".").flatMap { $0 == base.startIndex ? nil : $0 }
                    let stem = dot.map { String(base[..<$0]) } ?? base
                    let ext = dot.map { String(base[$0...]) } ?? ""
                    var n = 2
                    while exists("\(stem) (\(n))\(ext)") { n += 1 }
                    name = "\(stem) (\(n))\(ext)"
                case "overwrite":
                    let path = gamePath(system, folder: folder, name: name)
                    if let old = system.games.first(where: { $0.path.lowercased() == path.lowercased() }) {
                        removeGame(system: system, game: old, keepSave: false, reason: "overwrite")
                    }
                default:
                    return .error(409, code: "conflict", message: "Target exists",
                                  details: ["existing": "Roms/\(system.folder)/\(gamePath(system, folder: folder, name: name))"])
                }
            }
            let path = gamePath(system, folder: folder, name: name)
            let game = DemoLibrary.Game(path: path, sizeBytes: sizeBytes, modifiedAt: Date())
            lock.withLock {
                if let index = _library.systems.firstIndex(where: { $0.id == system.id }) {
                    _library.systems[index].games.append(game)
                }
            }
            return .json(200, ["gameId": DemoLibrary.id(system.folder + "/" + path)])
        case .save:
            return .json(200, ["backup": [
                "id": Self.randomId("bk_"), "label": NSNull(), "createdAt": ProtocolJSON.formatDate(Date()),
                "sizeBytes": 131_072, "reason": "before-replace",
            ]])
        case .state(_, let slot):
            return .json(200, ["slot": [
                "slot": slot, "sizeBytes": sizeBytes, "modifiedAt": ProtocolJSON.formatDate(Date()),
                "hasThumbnail": false, "locked": false, "lockStatus": NSNull(),
            ]])
        case .staging:
            return .json(200, ["stagingId": Self.randomId("st_")])
        }
    }

    /// Moves a game to the trash and drops it from the library.
    @discardableResult
    fileprivate func removeGame(system: DemoLibrary.System, game: DemoLibrary.Game, keepSave: Bool, reason: String) -> [String: Any] {
        let files = Self.files(system: system, game: game).filter { file in
            let role = file["role"] as? String
            return !(keepSave && (role == "save" || role == "state"))
        }
        let moved = files.compactMap { $0["path"] as? String }
        let bytes = files.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
        let now = Date()
        let stamp = ProtocolJSON.formatDate(now).replacingOccurrences(of: "-", with: "").replacingOccurrences(of: ":", with: "")
        let id = "tr_\(stamp)_\(UUID().uuidString.prefix(4).lowercased())"
        let entry: [String: Any] = [
            "id": id, "trashedAt": ProtocolJSON.formatDate(now), "reason": reason, "label": game.name,
            "files": moved, "sizeBytes": bytes, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
        ]
        lock.withLock {
            if let index = _library.systems.firstIndex(where: { $0.id == system.id }) {
                _library.systems[index].games.removeAll { $0.path == game.path }
            }
            trashEntries.insert(entry, at: 0)
        }
        return ["trashEntryId": id, "moved": moved, "movedBytes": bytes]
    }

    func deleteGame(id: String, keepSave: Bool) -> FakeResponse {
        guard let (system, game) = library.game(id: id) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        return .json(200, removeGame(system: system, game: game, keepSave: keepSave, reason: "delete-game"))
    }

    func createDownload(body: Data) -> FakeResponse {
        guard let object = try? JSONSerialization.jsonObject(with: body) as? [String: Any],
              let string = object["url"] as? String, let url = URL(string: string),
              let scheme = url.scheme?.lowercased(), ["http", "https"].contains(scheme), url.host() != nil
        else { return .error(400, code: "invalid_request", message: "Invalid URL") }
        let target: Target
        switch parseTarget(object["target"], fileNameRequired: false) {
        case .ok(let t): target = t
        case .refused(let response): return response
        }
        let now = Date()
        let job = FakeJob(
            id: Self.randomId("job_"), kind: "download", state: "queued", done: 0, total: nil,
            createdAt: now, updatedAt: now, result: nil, error: nil, target: target,
            // A URL whose last path component is "missing" fails (demo and tests).
            fails: url.lastPathComponent == "missing"
        )
        lock.withLock { jobs[job.id] = job }
        _ = string
        return .json(202, ["jobId": job.id])
    }

    /// Returns a job, moving it one step forward (queued → running → a
    /// third of the file per call → succeeded or failed).
    fileprivate func advance(_ id: String) -> [String: Any]? {
        let total = downloadSize
        guard var job = lock.withLock({ jobs[id] }) else { return nil }
        let now = Date()
        switch job.state {
        case "queued":
            job.state = "running"
            job.total = total
        case "running":
            job.done = min(total, job.done + max(1, total / 3))
            if job.fails {
                job.state = "failed"
                job.error = ["code": "not_found", "message": "Download failed: HTTP 404", "details": [String: Any]()]
            } else if job.done >= total {
                var name: String?
                if case .rom(_, _, let n) = job.target { name = n }
                if case .staging(let n) = job.target { name = n }
                let response = store(target: job.target, sizeBytes: total, onConflict: "rename", fallbackName: name ?? "download.bin")
                if let result = try? JSONSerialization.jsonObject(with: response.body) as? [String: Any], response.status == 200 {
                    job.state = "succeeded"
                    job.result = result
                    lock.withLock { _freeBytes -= total }
                } else {
                    job.state = "failed"
                    job.error = ["code": "internal", "message": "Store failed", "details": [String: Any]()]
                }
            }
        default:
            break
        }
        job.updatedAt = now
        lock.withLock { jobs[id] = job }
        return job.json
    }

    fileprivate func cancelJob(_ id: String) -> [String: Any]? {
        lock.withLock {
            guard var job = jobs[id] else { return nil }
            if job.state == "queued" || job.state == "running" {
                job.state = "cancelled"
                job.updatedAt = Date()
                jobs[id] = job
            }
            return job.json
        }
    }
}
#endif
