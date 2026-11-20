// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import CryptoKit
import Foundation
import ReminisLinkKit

/// An in-memory pak that follows protocol/openapi.yaml for the routes the
/// app uses so far: `/hello`, `/pair`, signed `/device`, `/systems`,
/// `/systems/{id}/games`, `/games/{id}` (GET and DELETE), `/games/{id}/art`
/// (GET, and PUT and DELETE of milestone M8, see FakePakArt.swift),
/// `/trash`, `DELETE /clients/me`, and the transfers of milestone M4:
/// `/uploads` (create, chunks with `Upload-Offset`, status, complete with
/// the SHA-256 check, cancel), `/downloads` and `/jobs`, and the saves,
/// states, locks, resume history, cheats and `/sd/files` of milestone M5
/// (see FakePakGameData.swift), and `POST /games/{id}/patch` with
/// `/sd/files?type=patch` of milestone M6 (see FakePakPatch.swift), and
/// `GET /games` and `GET /games/{id}/files/{fileId}` of milestone M11 (see
/// FakePakLibrary.swift, with the closing of the pak).
///
/// Uploads keep only a running SHA-256, never the bytes, so large files
/// cost no memory. Faults can be injected (lost connection before or after
/// a chunk is stored, restart of the pak, low free space).
///
/// It checks signatures like the pak (unknown client, clock skew, replayed
/// nonce, Ed25519 signature with the public key given at pairing, body
/// hash, duplicate query keys), so the app and the tests exercise the real
/// signing path. It serves no TLS (URLProtocol): its certificate pin
/// (``pin``) is carried by the pairing link and stored like a real one,
/// but never checked; the pinning is tested on its own (TLSPin). Its example values come from the
/// examples of openapi.yaml, with made-up game names.
public final class FakePak: FakeLateArrivals, @unchecked Sendable {
    public static let demoHost = "demo.reminislink.invalid"
    public static let demoPort: UInt16 = 47821
    public static let demoDeviceId = "5f1e2d3c-4b5a-4968-8776-a5b4c3d2e1f0"
    /// Fixed so the demo pairing link is stable (bytes 0x00 to 0x1f).
    public static let demoSecret = Data((0..<32).map { UInt8($0) })
    /// The certificate pin of the demo console (32 bytes, b64u). Never
    /// checked: the fake pak serves no TLS.
    public static let demoPin = Base64URL.encode(Data((0..<32).map { UInt8(255 - $0) }))
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
    /// Changed by ``rename(to:)`` (Settings > Console name, M11).
    public var deviceName: String { lock.withLock { _deviceName } }
    private var _deviceName: String
    public let model: DeviceModel
    public let secret: Data
    /// The certificate pin given by the pairing link.
    public let pin: String
    public let host: String
    public let port: UInt16
    public var library: DemoLibrary { lock.withLock { _library } }
    public let minClientVersion: String
    public let apiVersion: Int

    let lock = NSLock()
    private var _clockOffset: TimeInterval = 0
    private var _responseDelay: TimeInterval = 0
    private var _pairDelay: TimeInterval = 0
    private var nextPairDelays: [TimeInterval] = []
    private var _pairingOpen = true
    private var closePairingAfterHello = false
    /// Public keys of the paired clients (M13: the pak only keeps these).
    var clients: [String: Curve25519.Signing.PublicKey] = [:]
    var clientNames: [String: String] = [:]
    /// `platform` given at pairing, and the last `X-RL-Platform` seen.
    var clientPlatforms: [String: String] = [:]
    var lastRequestPlatform: [String: String] = [:]
    /// M13: writes refused as busy (see FakePakClients.swift).
    var busyWrites = 0
    var busyRetryAfter = 1
    var _busyRefusals = 0
    /// Last accepted signed request per client (`connectedClients`).
    var lastSeen: [String: Date] = [:]
    /// Clients disconnected from the console (M10, see FakePakClients.swift).
    var disconnectedClients: Set<String> = []
    /// Answers handled at once but delivered late, by method and path.
    private var lateAnswers: [(method: String, path: String, delay: TimeInterval)] = []
    /// Requests held before they reach the pak, and answers held after it
    /// handled them, by method and path.
    private var heldArrivals: [(method: String, path: String, gate: FakeGate)] = []
    private var heldAnswers: [(method: String, path: String, gate: FakeGate)] = []
    private var nonces = Set<String>()
    private var _log: [LoggedRequest] = []
    private var queued: [FakeResponse] = []
    var trashEntries: [[String: Any]]
    var _library: DemoLibrary
    /// Saves, states, locks and cheats of the games (milestone M5), made
    /// from the library counters the first time a game is used.
    var gameData: [String: FakeGameData] = [:]
    /// Arts set with `PUT /games/{id}/art` (M8), by game id.
    var customArt: [String: Data] = [:]
    /// Arts removed with `DELETE /games/{id}/art`.
    var removedArt: Set<String> = []
    var _trashedArtCount = 0
    /// Content of completed staging uploads (small files only), by upload
    /// id: what `cheats/import` with `source.type = "upload"` reads.
    var stagedUploads: [String: Data] = [:]
    /// Staging files by `stagingId` (milestone M6): their name, and their
    /// bytes when small enough to be kept.
    var stagedFiles: [String: FakeStagedFile] = [:]
    /// The next patch job finds its ROM changed and fails `409 conflict`.
    var _romChangesDuringNextPatch = false
    /// The next `PUT /cheats` finds the file changed on the console (412).
    var _conflictOnNextCheatsSave = false
    /// `GET /saves` calls so far, and the call that finds a save changed
    /// on the console (milestone M9, UI tests).
    var _saveListings = 0
    var _changeSaveOnListing: Int?
    private var uploads: [String: FakeUpload] = [:]
    var jobs: [String: FakeJob] = [:]
    private var chunkFaults: [ChunkFault] = []
    private var completeFaults = 0
    private var lostCompleteRequests = 0
    private var _chunkSize: Int64 = 4 << 20
    var _freeBytes: Int64 = 40_265_318_400
    private var _downloadSize: Int64 = 8 << 20
    private var _chunkLog: [ChunkRecord] = []
    /// Milestone M11 (FakePakLibrary.swift): `GET /games`, `fileId` and the
    /// file downloads exist (false: a pak older than M11, `404`), and
    /// `GET /games` lists at most this many games.
    var _allGamesSupported = true
    var _allGamesLimit = 20_000
    /// Whether the pak is open, quitting (`503 shutting_down`), or gone.
    var _availability: Availability = .open
    /// The request that makes the pak quit (demo mode, UI tests).
    var _closeTrigger: (method: String, path: String)?
    var _reopenDelay: TimeInterval?
    /// Called when the pak quits (its Bonjour service is withdrawn) and
    /// when it opens again.
    var _onAvailabilityChange: (@Sendable (Availability) -> Void)?
    /// The event streams of milestone M14 (FakePakEvents.swift).
    let eventHub = FakeEventHub()

    public init(
        deviceId: String = FakePak.demoDeviceId,
        deviceName: String = "Demo Brick",
        model: DeviceModel = .brick,
        secret: Data = FakePak.demoSecret,
        pin: String = FakePak.demoPin,
        host: String = FakePak.demoHost,
        port: UInt16 = FakePak.demoPort,
        library: DemoLibrary = .sample,
        minClientVersion: String = "1.0.0",
        apiVersion: Int = APIClient.apiVersion
    ) {
        self.deviceId = deviceId
        self._deviceName = deviceName
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

    /// The link of the QR code (pairing v2), with the pin, and the
    /// endpoint when the host is an IPv4 address (as `h` must be; the demo
    /// host is a name, found through the demo discovery).
    public var pairingLink: PairingLink {
        PairingLink(
            deviceId: deviceId, deviceName: deviceName, secret: secret,
            endpoint: PairingLink.isIPv4(host) ? endpoint : nil, pin: pin
        )
    }

    /// What a Bonjour browser would see.
    public var discoveredDevice: DiscoveredDevice {
        DiscoveredDevice(
            id: deviceId, name: deviceName, model: model, platform: "tg5040", os: "nextui",
            osVersion: "NextUI-20261013-039a06de", pakVersion: "1.0.0", apiVersion: apiVersion,
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

    /// Whether the pairing screen is shown on the console (M8: pairing is
    /// only open while the QR code is shown). Closed, `/hello`
    /// says `pairingOpen: false` and `/pair` answers `401 pairing_failed`.
    public var pairingOpen: Bool {
        get { lock.withLock { _pairingOpen } }
        set { lock.withLock { _pairingOpen = newValue } }
    }

    /// The pairing screen closes right after the next `/hello` (which still
    /// says it is open).
    public func closePairingAfterNextHello() {
        lock.withLock { closePairingAfterHello = true }
    }

    /// The next `POST /pair` requests wait these delays (in their order of
    /// arrival) after registering the client, instead of ``pairDelay``.
    public func delayNextPairs(_ delays: [TimeInterval]) {
        lock.withLock { nextPairDelays.append(contentsOf: delays) }
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

    /// The next `count` successful `complete` requests write the file, then
    /// their answer is lost.
    public func dropCompleteAnswers(_ count: Int) {
        lock.withLock { completeFaults += count }
    }

    /// The next `count` `complete` requests are lost before reaching the
    /// pak (nothing is written, the upload stays).
    public func loseCompleteRequests(_ count: Int) {
        lock.withLock { lostCompleteRequests += count }
    }

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
            stagedUploads = [:]
            jobs = [:]
            nonces = []
            lastSeen = [:]
            // Kept in memory only (M10).
            disconnectedClients = []
        }
        // M14: the streams end (no `shutdown`: as after a crash).
        endEventStreams(shutdown: nil)
    }

    /// The console is renamed (Settings > Console name): `/hello`,
    /// `/device` and the Bonjour instance name change.
    public func rename(to name: String) {
        lock.withLock { _deviceName = name }
    }

    public var clientIds: [String] { lock.withLock { clients.keys.sorted() } }

    /// Registers a client directly, as if it had paired.
    public func addClient(id: String, publicKey: Curve25519.Signing.PublicKey, name: String = "Test iPhone", platform: String = "apple") {
        lock.withLock {
            clients[id] = publicKey
            clientNames[id] = name
            clientPlatforms[id] = platform
        }
    }

    /// Registers an identity directly, as if it had paired.
    public func addClient(_ identity: ClientIdentity, name: String = "Test iPhone") {
        addClient(id: identity.clientId, publicKey: identity.privateKey.publicKey, name: name)
    }

    /// The next request with this method and path (e.g. `DELETE`,
    /// `/api/v1/games/...`) is handled at once (its effect is immediate) but
    /// its answer is delivered `delay` seconds later, without blocking the
    /// other requests: an answer that arrives after later ones.
    public func delayNextAnswer(method: String, path: String, by delay: TimeInterval) {
        lock.withLock { lateAnswers.append((method, path, delay)) }
    }

    /// The next request with this method and path waits before it reaches
    /// the pak until the returned gate opens: requests sent after it may be
    /// handled first. `gate.reached` says it is pending.
    @discardableResult
    public func holdNextArrival(method: String, path: String) -> FakeGate {
        let gate = FakeGate()
        lock.withLock { heldArrivals.append((method, path, gate)) }
        return gate
    }

    /// The next request with this method and path is handled at once (its
    /// effect is immediate), but its answer waits until the returned gate
    /// opens. `gate.reached` says it was handled.
    @discardableResult
    public func holdNextAnswer(method: String, path: String) -> FakeGate {
        let gate = FakeGate()
        lock.withLock { heldAnswers.append((method, path, gate)) }
        return gate
    }

    public func arrivalGate(for request: URLRequest) -> FakeGate? {
        let method = (request.httpMethod ?? "GET").uppercased()
        guard let path = request.url?.path(percentEncoded: true) else { return nil }
        return lock.withLock {
            guard let index = heldArrivals.firstIndex(where: { $0.method == method && $0.path == path }) else { return nil }
            return heldArrivals.remove(at: index).gate
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
        let touchedSystem = systemTouched(path: path)
        var response = route(request, method: method, path: path, query: query, body: body)
        // M14: what this request changed, for the event streams.
        if let systemIds = libraryChange(method: method, path: path, response: response, touchedSystem: touchedSystem) {
            emitLibrary(systemIds)
        }
        checkDeviceChange()
        lock.withLock {
            if let index = lateAnswers.firstIndex(where: { $0.method == method && $0.path == path }) {
                response.delay = max(response.delay, lateAnswers.remove(at: index).delay)
            }
            if let index = heldAnswers.firstIndex(where: { $0.method == method && $0.path == path }) {
                response.gate = heldAnswers.remove(at: index).gate
            }
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
        if let refusal = availabilityRefusal(method: method, path: path) {
            return refusal
        }

        switch (method, segments) {
        case ("GET", ["hello"]):
            return hello()
        case ("POST", ["pair"]):
            return pair(body: body)
        default:
            break
        }

        if let failure = verify(request, method: method, path: path, query: query, body: body, segments: segments) {
            return failure
        }
        let clientId = request.value(forHTTPHeaderField: SignedHeaders.client) ?? ""
        // M14: before the queued answers, which are meant for the requests
        // of the screens (a stream opens whenever the app is active).
        if method == "GET" && segments == ["events"] {
            return openEventStream(clientId: clientId)
        }
        if let next = lock.withLock({ queued.isEmpty ? nil : queued.removeFirst() }) {
            return next
        }
        let params = Dictionary(uniqueKeysWithValues: Self.decodeQuery(query))

        switch (method, segments) {
        case ("GET", ["device"]):
            return device(clientId: clientId)
        case ("DELETE", ["clients", "me"]):
            lock.withLock {
                _ = clients.removeValue(forKey: clientId)
                lastSeen[clientId] = nil
            }
            endEventStreams(clientId: clientId)
            return FakeResponse(status: 204)
        case ("POST", ["clients", "me", "connect"]):
            return connectClient(clientId: clientId)
        case ("GET", ["systems"]):
            return systems()
        case ("GET", ["saves"]):
            return listSaves()
        case ("GET", let s) where s.count == 3 && s[0] == "systems" && s[2] == "games":
            return games(systemId: s[1], folderId: params["folderId"])
        case ("GET", ["games"]):
            return allGames()
        case ("GET", let s) where s.count == 4 && s[0] == "games" && s[2] == "files":
            return downloadFile(gameId: s[1], fileId: s[3])
        case ("GET", let s) where s.count == 2 && s[0] == "games":
            return game(id: s[1])
        case ("DELETE", let s) where s.count == 2 && s[0] == "games":
            return deleteGame(id: s[1], keepSave: params["keepSave"] == "true")
        case ("GET", let s) where s.count == 3 && s[0] == "games" && s[2] == "deletion":
            return deletionPreview(id: s[1], keepSave: params["keepSave"] == "true")
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
        case ("PUT", let s) where s.count == 3 && s[0] == "games" && s[2] == "art":
            return putArt(gameId: s[1], request: request, body: body)
        case ("DELETE", let s) where s.count == 3 && s[0] == "games" && s[2] == "art":
            return deleteArt(gameId: s[1])
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
            if method == "POST", segments.count == 3, segments[0] == "games", segments[2] == "patch" {
                return createPatchJob(gameId: segments[1], body: body)
            }
            if let response = routeGameData(request, method: method, segments: segments, params: params, body: body) {
                return response
            }
            return .error(404, code: "not_found", message: "No route")
        }
    }

    /// The pak's checks, in the order of the contract.
    private func verify(
        _ request: URLRequest, method: String, path: String, query: String, body: Data, segments: [String]
    ) -> FakeResponse? {
        func header(_ name: String) -> String? { request.value(forHTTPHeaderField: name) }
        guard let clientId = header(SignedHeaders.client), let publicKey = lock.withLock({ clients[clientId] }) else {
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
        ), Signer.isValidSignature(signature, publicKey: publicKey, canonical: canonical)
        else { return .error(401, code: "bad_signature", message: "Bad signature") }
        let seen = now()
        let platform = header(RequestPlatform.header).flatMap(RequestPlatform.init(rawValue:))
        lock.withLock {
            _ = nonces.insert(clientId + "|" + nonce)
            lastSeen[clientId] = seen
            if let platform { lastRequestPlatform[clientId] = platform.rawValue }
        }
        // M10, as the contract orders it: right after a valid signature,
        // `401 disconnected`, before the body is read.
        if let refusal = checkDisconnected(clientId: clientId, method: method, segments: segments) {
            return refusal
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
        // M13: once the body is verified, a write waits for its turn.
        return checkBusy(method: method, segments: segments)
    }

    // MARK: - Routes

    private func now() -> Date { Date().addingTimeInterval(clockOffset) }

    private func hello() -> FakeResponse {
        let pairingOpen = lock.withLock { () -> Bool in
            let open = _pairingOpen
            if closePairingAfterHello {
                closePairingAfterHello = false
                _pairingOpen = false
            }
            return open
        }
        return .json(200, [
            "deviceId": deviceId, "deviceName": deviceName, "model": model.rawValue,
            "platform": "tg5040", "os": "nextui", "osVersion": "NextUI-20261013-039a06de", "pakVersion": "1.0.0",
            "apiVersion": apiVersion, "minClientVersion": minClientVersion,
            "serverTime": ProtocolJSON.formatDate(now()), "pairingOpen": pairingOpen,
        ])
    }

    private func pair(body: Data) -> FakeResponse {
        struct Request: Decodable {
            let clientId: String
            let clientName: String
            let platform: String
            let clientPublicKey: String
            let clientNonce: String
            let proof: String
        }
        guard let request = try? JSONDecoder().decode(Request.self, from: body),
              request.clientId.range(of: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", options: .regularExpression) != nil,
              (1...64).contains(request.clientName.unicodeScalars.count),
              ["apple", "android", "web", "other"].contains(request.platform),
              let keyBytes = Base64URL.decode(request.clientPublicKey), keyBytes.count == 32,
              let publicKey = try? Curve25519.Signing.PublicKey(rawRepresentation: keyBytes),
              let clientNonce = Base64URL.decode(request.clientNonce), clientNonce.count == 32
        else { return .error(400, code: "invalid_request", message: "Invalid pairing request") }
        // Closed: refused without counting a failure (docs/specs/05 §7.1).
        guard pairingOpen else { return .error(401, code: "pairing_failed", message: "Pairing failed") }
        let message = Pairing.clientProofMessage(
            deviceId: deviceId, clientId: request.clientId, clientPublicKey: request.clientPublicKey,
            clientNonce: request.clientNonce
        )
        guard Pairing.isValidProof(request.proof, secret: secret, message: message) else {
            return .error(401, code: "pairing_failed", message: "Pairing failed")
        }
        let deviceNonce = Base64URL.encode(Pairing.makeNonce())
        let delay = lock.withLock { () -> TimeInterval in
            clients[request.clientId] = publicKey
            clientNames[request.clientId] = request.clientName
            clientPlatforms[request.clientId] = request.platform
            return nextPairDelays.isEmpty ? _pairDelay : nextPairDelays.removeFirst()
        }
        let deviceMessage = Pairing.deviceProofMessage(
            deviceId: deviceId, clientId: request.clientId, clientPublicKey: request.clientPublicKey,
            clientNonce: request.clientNonce, deviceNonce: deviceNonce
        )
        var response = FakeResponse.json(200, [
            "deviceId": deviceId, "deviceName": deviceName,
            "deviceNonce": deviceNonce,
            "proof": Pairing.proof(secret: secret, message: deviceMessage),
        ])
        // Delivered later, without blocking the other requests.
        response.delay = delay
        return response
    }

    /// `connectedClients`: a signed request in the last 30 s, or an open
    /// event stream (M14, `lastSeenAt` is then now).
    func connectedClientsJSON() -> [[String: Any]] {
        let current = now()
        let limit = current.addingTimeInterval(-30)
        let streaming = Set(eventHub.streamingClients)
        return lock.withLock { () -> [[String: Any]] in
            var seen = lastSeen.filter { $0.value >= limit && clients[$0.key] != nil }
            for id in streaming where clients[id] != nil { seen[id] = current }
            return seen
                .map { (name: clientNames[$0.key] ?? "", seen: $0.value) }
                .sorted { $0.name < $1.name }
                .map { ["clientName": $0.name, "lastSeenAt": ProtocolJSON.formatDate($0.seen)] }
        }
    }

    private func device(clientId: String) -> FakeResponse {
        let connected = connectedClientsJSON()
        return .json(200, [
            "deviceId": deviceId, "deviceName": deviceName, "model": model.rawValue,
            "platform": "tg5040", "os": "nextui", "osVersion": "NextUI-20261013-039a06de", "pakVersion": "1.0.0",
            "network": ["ipv4": "192.168.1.42", "ssid": "DemoNet"],
            "battery": batteryJSON,
            "storage": storageJSON,
            "uptimeSeconds": 512,
            "connectedClients": connected,
        ])
    }

    private func systems() -> FakeResponse {
        let list = library.systems
            .sorted { $0.label.localizedCaseInsensitiveCompare($1.label) == .orderedAscending }
            .map { s -> [String: Any] in
                ["id": s.id, "label": s.label, "folder": s.folder, "tag": s.tag, "gameCount": s.games.count,
                 "emulatorInstalled": s.emulatorInstalled]
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
        summaryJSON(system: system, game: game)
    }

    /// A game as `GET /systems/{id}/games` lists it.
    func summaryJSON(system: DemoLibrary.System, game: DemoLibrary.Game) -> [String: Any] {
        let data = currentData(system: system, game: game)
        let id = DemoLibrary.id(system.folder + "/" + game.path)
        return [
            "id": id, "name": game.name, "fileName": game.fileName,
            "kind": game.kind.rawValue, "sizeBytes": game.sizeBytes,
            "modifiedAt": ProtocolJSON.formatDate(game.modifiedAt), "hasSave": data.save != nil,
            "stateCount": data.slots.values.filter { $0.state != nil }.count,
            "lockedStateCount": data.slots.values.filter { $0.lockCopy != nil }.count,
            "cheatCount": data.cheats.count, "hasArt": hasArt(gameId: id, game: game),
            "futureField": "ignored by the app",
        ]
    }

    private func game(id: String) -> FakeResponse {
        guard let (system, game) = library.game(id: id) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        let files = self.files(system: system, game: game)
        let total = files.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
        return .json(200, [
            "id": id, "systemId": system.id, "name": game.name, "fileName": game.fileName,
            "kind": game.kind.rawValue, "files": files, "totalBytes": total,
        ])
    }

    /// The files of a game, as `GET /games/{id}` lists them.
    func files(system: DemoLibrary.System, game: DemoLibrary.Game) -> [[String: Any]] {
        let date = ProtocolJSON.formatDate(game.modifiedAt)
        let data = currentData(system: system, game: game)
        var files: [[String: Any]] = [
            ["role": "rom", "path": "Roms/\(system.folder)/\(game.path)", "sizeBytes": game.sizeBytes, "modifiedAt": date],
        ]
        if let save = data.save {
            files.append(["role": "save", "path": Self.savePath(system, game), "sizeBytes": save.size,
                          "modifiedAt": ProtocolJSON.formatDate(data.saveModifiedAt)])
        }
        for slot in data.slots.keys.sorted() {
            guard let state = data.slots[slot]?.state else { continue }
            files.append([
                "role": "state", "slot": slot, "path": Self.statePath(system, game, slot: slot),
                "sizeBytes": state.size, "modifiedAt": ProtocolJSON.formatDate(data.slots[slot]?.modifiedAt ?? game.modifiedAt),
            ])
        }
        if !data.cheats.isEmpty {
            // The size of the `.cht` the fake pak serves (M11).
            files.append(["role": "cheats", "path": Self.cheatsPath(system, game),
                          "sizeBytes": Self.chtText(data.cheats).count, "modifiedAt": date])
        }
        if let art = currentArt(gameId: DemoLibrary.id(system.folder + "/" + game.path), game: game) {
            files.append(["role": "art", "path": "Roms/\(system.folder)/.media/\(game.name).png", "sizeBytes": Int64(art.data.count), "modifiedAt": date])
        }
        return lock.withLock { _allGamesSupported } ? Self.withFileIds(files) : files
    }

    private func art(gameId: String, ifNoneMatch: String?) -> FakeResponse {
        guard let (_, game) = library.game(id: gameId), let art = currentArt(gameId: gameId, game: game) else {
            return .error(404, code: "not_found", message: "No art")
        }
        if ifNoneMatch == art.etag {
            return FakeResponse(status: 304, headers: ["ETag": art.etag])
        }
        return FakeResponse(status: 200, headers: [
            "Content-Type": "image/png", "ETag": art.etag, "Cache-Control": "private, max-age=86400",
        ], body: art.data)
    }

    private func trash() -> FakeResponse {
        let entries = lock.withLock { trashEntries }
        let total = entries.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
        return .json(200, ["entries": entries, "totalBytes": total])
    }

    static func size(_ value: Any?) -> Int64 {
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
    /// The bytes of a small staging upload (a `.cht` to import).
    var bytes: Data?
}

struct FakeJob {
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
    /// The downloaded URL (download jobs).
    var url: String? = nil
    /// What a patch job does (milestone M6).
    var patch: FakePatchPlan? = nil
    /// `DELETE` arrived while running: the job ends `cancelled` at its
    /// next step (M6), unless it was already done.
    var cancelRequested = false

    var json: [String: Any] {
        [
            "id": id, "kind": kind, "state": state,
            "progress": ["done": done, "total": total.map { $0 as Any } ?? NSNull()],
            "createdAt": ProtocolJSON.formatDate(createdAt), "updatedAt": ProtocolJSON.formatDate(updatedAt),
            "result": result.map { $0 as Any } ?? NSNull(), "error": error.map { $0 as Any } ?? NSNull(),
            "cancelRequested": cancelRequested,
        ]
    }
}

extension FakePak {
    /// A parsed `target`.
    enum Target {
        case rom(system: DemoLibrary.System, folder: String, fileName: String?)
        case save(gameId: String)
        case state(gameId: String, slot: Int)
        case staging(fileName: String?)
    }

    fileprivate enum ParsedTarget {
        case ok(Target)
        case refused(FakeResponse)
    }

    static func randomId(_ prefix: String) -> String {
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

    func gamePath(_ system: DemoLibrary.System, folder: String, name: String) -> String {
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
            var upload = FakeUpload(target: target, sizeBytes: size, sha256: sha, onConflict: onConflict)
            if case .staging = target, size <= 1 << 20 { upload.bytes = Data() }
            uploads[id] = upload
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
            upload.bytes?.append(body)
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
        let lost = lock.withLock { () -> Bool in
            guard lostCompleteRequests > 0 else { return false }
            lostCompleteRequests -= 1
            return true
        }
        if lost { return .networkFailure(.networkConnectionLost) }
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
            let drop = lock.withLock { () -> Bool in
                _ = uploads.removeValue(forKey: id)
                if let bytes = upload.bytes {
                    stagedUploads[id] = bytes
                    if let object = try? JSONSerialization.jsonObject(with: response.body) as? [String: Any],
                       let stagingId = object["stagingId"] as? String {
                        stagedUploads[stagingId] = bytes
                    }
                }
                if case .staging(let name) = upload.target,
                   let object = try? JSONSerialization.jsonObject(with: response.body) as? [String: Any],
                   let stagingId = object["stagingId"] as? String {
                    stagedFiles[stagingId] = FakeStagedFile(name: name ?? "upload.bin", bytes: upload.bytes)
                }
                _freeBytes -= upload.sizeBytes
                guard completeFaults > 0 else { return false }
                completeFaults -= 1
                return true
            }
            if drop { return .networkFailure(.networkConnectionLost) }
        }
        return response
    }

    /// Writes a received file to its destination (upload or download).
    func store(target: Target, sizeBytes: Int64, onConflict: String, fallbackName: String = "download.bin") -> FakeResponse {
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
        case .save(let gameId):
            return .json(200, ["backup": replaceSave(gameId: gameId, with: FakeBlob(size: sizeBytes, digest: Self.randomId("up-"))).map { $0 as Any } ?? NSNull()])
        case .state(let gameId, let slot):
            guard let json = storeState(gameId: gameId, slot: slot, blob: FakeBlob(size: sizeBytes, digest: Self.randomId("up-"))) else {
                return .error(409, code: "state_locked", message: "State is locked")
            }
            return .json(200, ["slot": json])
        case .staging:
            return .json(200, ["stagingId": Self.randomId("st_")])
        }
    }

    /// Moves a game to the trash and drops it from the library.
    @discardableResult
    func removeGame(system: DemoLibrary.System, game: DemoLibrary.Game, keepSave: Bool, reason: String) -> [String: Any] {
        let files = deletionSelection(system: system, game: game, keepSave: keepSave).moved
        let moved = files.compactMap { $0["path"] as? String }
        let bytes = files.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
        let now = Date()
        let stamp = ProtocolJSON.formatDate(now).replacingOccurrences(of: "-", with: "").replacingOccurrences(of: ":", with: "")
        let id = "tr_\(stamp)_\(UUID().uuidString.prefix(4).lowercased())"
        let entry: [String: Any] = [
            "id": id, "trashedAt": ProtocolJSON.formatDate(now), "reason": reason, "label": game.name,
            "files": moved, "sizeBytes": bytes, "expiresAt": ProtocolJSON.formatDate(now.addingTimeInterval(30 * 86_400)),
        ]
        let key = Self.dataKey(system: system, game: game)
        // The files as they are now (made from the seed if never read).
        _ = currentData(system: system, game: game)
        lock.withLock {
            var data = gameData[key]
            gameData[key] = nil
            // As the pak (internal/library/write.go): the cheats always go
            // to the trash; the save, the states, their locks and history
            // too, unless keepSave. What goes is gone for a game sharing
            // these files: an explicit empty state, never the seed again.
            if keepSave {
                data?.cheats = []
            } else {
                data = FakeGameData(saveModifiedAt: now)
            }
            if let index = _library.systems.firstIndex(where: { $0.id == system.id }) {
                _library.systems[index].games.removeAll { $0.path == game.path }
                // A game sharing these files keeps them, under its own key
                // now that the first of them may be gone.
                let remaining = _library.systems[index]
                if let twin = remaining.games.first(where: { $0.fileName.lowercased() == game.fileName.lowercased() }) {
                    gameData[Self.dataKey(system: remaining, game: twin)] = data
                } else if let data {
                    gameData[key] = data
                }
            }
            trashEntries.insert(entry, at: 0)
        }
        return ["trashEntryId": id, "moved": moved, "movedBytes": bytes]
    }

    /// The files a deletion moves and keeps (the same selection for the
    /// preview and for `DELETE /games/{id}`).
    func deletionSelection(system: DemoLibrary.System, game: DemoLibrary.Game, keepSave: Bool)
        -> (moved: [[String: Any]], kept: [[String: Any]]) {
        let files = self.files(system: system, game: game)
        let isKept = { (file: [String: Any]) -> Bool in
            let role = file["role"] as? String
            return keepSave && (role == "save" || role == "rtc" || role == "state")
        }
        return (files.filter { !isKept($0) }, files.filter(isKept))
    }

    /// `GET /games/{id}/deletion` (M8): names relative to the system folder
    /// under `Roms/`, to the SD card otherwise.
    func deletionPreview(id: String, keepSave: Bool) -> FakeResponse {
        guard let (system, game) = library.game(id: id) else {
            return .error(404, code: "not_found", message: "Game not found")
        }
        let selection = deletionSelection(system: system, game: game, keepSave: keepSave)
        let prefix = "Roms/\(system.folder)/"
        let item = { (file: [String: Any]) -> [String: Any] in
            let path = file["path"] as? String ?? ""
            return ["name": path.hasPrefix(prefix) ? String(path.dropFirst(prefix.count)) : path,
                    "role": file["role"] as? String ?? "other", "size": Self.size(file["sizeBytes"])]
        }
        return .json(200, [
            "moved": selection.moved.map(item), "kept": selection.kept.map(item),
            "movedBytes": selection.moved.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) },
        ])
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
            fails: url.lastPathComponent == "missing", url: string
        )
        lock.withLock { jobs[job.id] = job }
        _ = string
        emitJob(job)
        return .json(202, ["jobId": job.id])
    }

    /// Returns a job, moving it one step forward (queued → running → a
    /// third of the file per call → succeeded or failed).
    func advance(_ id: String) -> [String: Any]? {
        guard let before = lock.withLock({ jobs[id] }) else { return nil }
        let json = advanceOnce(id)
        // M14: every change goes to the event streams.
        if let after = lock.withLock({ jobs[id] }),
           after.state != before.state || after.done != before.done || after.cancelRequested != before.cancelRequested {
            emitJob(after)
            if after.state == "succeeded" && before.state != "succeeded", let systemIds = librarySystems(of: after) {
                emitLibrary(systemIds)
            }
        }
        return json
    }

    private func advanceOnce(_ id: String) -> [String: Any]? {
        let total = downloadSize
        guard var job = lock.withLock({ jobs[id] }) else { return nil }
        let now = Date()
        if job.cancelRequested && job.state == "running" {
            // Stopped before installing anything.
            job.state = "cancelled"
        } else if job.patch != nil {
            advancePatch(&job)
            job.updatedAt = now
            lock.withLock { jobs[id] = job }
            return job.json
        }
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
                let fromURL = job.url.flatMap(URL.init(string:)).map(\.lastPathComponent).flatMap(Self.cleanFileName)
                let fileName = name ?? fromURL ?? "download.bin"
                let response = store(target: job.target, sizeBytes: total, onConflict: "rename", fallbackName: fileName)
                if let result = try? JSONSerialization.jsonObject(with: response.body) as? [String: Any], response.status == 200 {
                    job.state = "succeeded"
                    job.result = result
                    lock.withLock {
                        _freeBytes -= total
                        if let stagingId = result["stagingId"] as? String {
                            // A download keeps no bytes: only its name.
                            stagedFiles[stagingId] = FakeStagedFile(name: fileName, bytes: nil)
                        }
                    }
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

    func cancelJob(_ id: String) -> [String: Any]? {
        let json = cancelJobOnce(id)
        if let job = lock.withLock({ jobs[id] }) { emitJob(job) }
        return json
    }

    private func cancelJobOnce(_ id: String) -> [String: Any]? {
        lock.withLock {
            guard var job = jobs[id] else { return nil }
            // Queued: cancelled at once. Running: it stays running with
            // `cancelRequested` until its real end (docs: DELETE /jobs).
            if job.state == "queued" {
                job.state = "cancelled"
                job.updatedAt = Date()
            } else if job.state == "running" {
                job.cancelRequested = true
                job.updatedAt = Date()
            }
            jobs[id] = job
            return job.json
        }
    }
}
#endif
