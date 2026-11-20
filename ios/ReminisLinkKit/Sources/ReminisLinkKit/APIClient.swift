import CryptoKit
import Foundation

/// Host and port of a pak on the local network.
public struct Endpoint: Hashable, Sendable, Codable, CustomStringConvertible {
    public let host: String
    public let port: UInt16

    public init(host: String, port: UInt16) {
        self.host = host
        self.port = port
    }

    /// `host:port`, with brackets and an escaped zone for IPv6.
    public var authority: String {
        if host.contains(":") {
            return "[" + host.replacingOccurrences(of: "%", with: "%25") + "]:\(port)"
        }
        return "\(host):\(port)"
    }

    public var description: String { authority }
}

/// What a signed client needs for one paired device (M13): the identity
/// of the app, which signs, and the console's certificate pin.
public struct DeviceCredentials: Sendable {
    public let deviceId: String
    public let identity: ClientIdentity
    /// b64u(SHA-256(SPKI)) of the console's certificate.
    public let pin: String

    public init(deviceId: String, identity: ClientIdentity, pin: String) {
        self.deviceId = deviceId
        self.identity = identity
        self.pin = pin
    }

    public var clientId: String { identity.clientId }
}

/// Result of `GET /games/{gameId}/art`.
public enum ArtResponse: Sendable, Equatable {
    case image(Data, etag: String?)
    case notModified
    case none
}

/// Client of the pak API (protocol/openapi.yaml).
///
/// Every request goes over HTTPS, and the console must present the
/// certificate pinned at pairing (M13, ``PinnedTrust``): never system trust.
/// Every route but `/hello` and `/pair` is signed (RL2-ED25519). A new nonce
/// is drawn for every attempt: after `401 clock_skew`, the client asks
/// `/hello` for the device time, corrects its offset and signs the request
/// again, once.
public actor APIClient {
    public static let apiPrefix = "/api/v1"
    /// `apiVersion` of the paks this app speaks to (M13).
    public static let apiVersion = 2

    public nonisolated let endpoint: Endpoint
    /// The device this client expects to talk to (checked on `/hello`).
    public nonisolated let expectedDeviceId: String?
    private let credentials: DeviceCredentials?
    /// The certificate pin the console must present; nil refuses every
    /// TLS server.
    public nonisolated let pin: String?
    /// `X-RL-Platform`, informative.
    private let platform: RequestPlatform?
    private let session: URLSession
    private let now: @Sendable () -> Date
    private let appVersion: String
    /// Sees every error answer of the pak to a signed request, with the
    /// moment that request was sent (the session learns of
    /// `401 disconnected` and `503 shutting_down` whatever the screen or
    /// transfer that sent the request).
    private let errorObserver: (@Sendable (ServerError, ContinuousClock.Instant) -> Void)?

    /// Device time minus local time, measured on the last `/hello`.
    public private(set) var clockOffset: TimeInterval = 0

    /// `https`. Only the package's tests use `http`, against a local test
    /// server (TLS needs a certificate they cannot install).
    nonisolated let scheme: String

    public init(
        endpoint: Endpoint,
        expectedDeviceId: String? = nil,
        credentials: DeviceCredentials? = nil,
        pin: String? = nil,
        platform: RequestPlatform? = nil,
        appVersion: String = "1.0.0",
        session: URLSession = APIClient.makeSession(),
        now: @escaping @Sendable () -> Date = { Date() },
        errorObserver: (@Sendable (ServerError, ContinuousClock.Instant) -> Void)? = nil
    ) {
        self.init(
            scheme: "https", endpoint: endpoint, expectedDeviceId: expectedDeviceId, credentials: credentials, pin: pin,
            platform: platform, appVersion: appVersion, session: session, now: now, errorObserver: errorObserver
        )
    }

    init(
        scheme: String,
        endpoint: Endpoint,
        expectedDeviceId: String? = nil,
        credentials: DeviceCredentials? = nil,
        pin: String? = nil,
        platform: RequestPlatform? = nil,
        appVersion: String = "1.0.0",
        session: URLSession = APIClient.makeSession(),
        now: @escaping @Sendable () -> Date = { Date() },
        errorObserver: (@Sendable (ServerError, ContinuousClock.Instant) -> Void)? = nil
    ) {
        self.scheme = scheme
        self.endpoint = endpoint
        self.expectedDeviceId = expectedDeviceId ?? credentials?.deviceId
        self.credentials = credentials
        self.pin = pin ?? credentials?.pin
        self.platform = platform
        self.appVersion = appVersion
        self.session = session
        self.now = now
        self.errorObserver = errorObserver
    }

    /// An ephemeral session (no shared cache, no cookies). Tests and the demo
    /// mode pass their own `URLProtocol` classes.
    public static func makeSession(protocolClasses: [AnyClass] = []) -> URLSession {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 15
        configuration.waitsForConnectivity = false
        configuration.requestCachePolicy = .reloadIgnoringLocalCacheData
        configuration.urlCache = nil
        if !protocolClasses.isEmpty {
            configuration.protocolClasses = protocolClasses + (configuration.protocolClasses ?? [])
        }
        return URLSession(configuration: configuration)
    }

    // MARK: - Unsigned routes

    /// `GET /hello`: checks the device id and the compatibility, and measures
    /// the clock offset used to sign the next requests.
    ///
    /// `timeout`: the longest silence before it fails, when shorter than
    /// the session's (M11: the probe sent when the console's Bonjour
    /// service disappears).
    @discardableResult
    public func hello(timeout: TimeInterval? = nil) async throws -> Hello {
        var request = try makeRequest(method: "GET", segments: ["hello"])
        if let timeout { request.timeoutInterval = timeout }
        let (data, response) = try await transport(request)
        try check(response, data: data, accept: [200])
        let hello: Hello = try decode(data)
        clockOffset = hello.serverTime.timeIntervalSince(now())
        if let expected = expectedDeviceId, hello.deviceId != expected {
            throw APIError.deviceMismatch(expected: expected, actual: hello.deviceId)
        }
        guard hello.apiVersion == Self.apiVersion, AppVersion.isSupported(appVersion, minimum: hello.minClientVersion) else {
            throw APIError.incompatible(apiVersion: hello.apiVersion, minClientVersion: hello.minClientVersion)
        }
        return hello
    }

    /// `POST /pair` (pairing v2), then checks the device proof. Returns the
    /// verified answer: store the pairing only then.
    @discardableResult
    public func pair(_ attempt: PairingAttempt) async throws -> PairResponse {
        var request = try makeRequest(method: "POST", segments: ["pair"])
        request.httpBody = try ProtocolJSON.encoder().encode(attempt.request)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let (data, response) = try await transport(request)
        try check(response, data: data, accept: [200])
        let pairResponse: PairResponse = try decode(data)
        do {
            try attempt.finish(pairResponse)
            return pairResponse
        } catch let error as PairingError {
            throw APIError.pairing(error)
        }
    }

    // MARK: - Device

    public func device() async throws -> DeviceStatus {
        try await signedJSON(method: "GET", segments: ["device"])
    }

    /// The longest silence of `GET /events` before URLSession gives up:
    /// beyond the liveness timeout (35 s), which ``EventStream`` watches.
    public static let eventsTimeout: TimeInterval = 60

    /// `GET /events` (M14): opens the signed event stream and returns its
    /// bytes. An error answer throws (`404` from a pak older than M14,
    /// `429` with `Retry-After`, `401`, `503`), and is seen by the error
    /// observer like any other. Cancelling the returned stream (or the
    /// task reading it) closes the connection.
    public func openEvents() async throws -> any EventByteSource {
        let ((bytes, errorBody), response) = try await withSignedRequests(
            method: "GET", segments: ["events"], query: [], headers: ["Accept": "text/event-stream"],
            body: nil, bodyFile: nil, bodyFileSHA256: nil, timeout: Self.eventsTimeout
        ) { request -> ((EventBytes?, Data), HTTPURLResponse, Data) in
            let (bytes, response): (URLSession.AsyncBytes, URLResponse)
            let delegate = PakTaskDelegate(pin: self.pin)
            do {
                (bytes, response) = try await self.session.bytes(for: request, delegate: delegate)
            } catch let error as URLError {
                throw delegate.failure(error)
            }
            guard let http = response as? HTTPURLResponse else {
                bytes.task.cancel()
                throw APIError.invalidResponse("not an HTTP response")
            }
            guard http.statusCode == 200 else {
                // An error body, small: read it, then close.
                var body = Data()
                do {
                    for try await byte in bytes {
                        body.append(byte)
                        if body.count >= 64 << 10 { break }
                    }
                } catch {}
                bytes.task.cancel()
                return ((nil, body), http, body)
            }
            return ((EventBytes(bytes: bytes), Data()), http, Data())
        }
        guard response.statusCode == 200, let bytes else {
            try check(response, data: errorBody, accept: [200])
            throw APIError.unexpectedStatus(response.statusCode)
        }
        return bytes
    }

    /// `DELETE /clients/me`. The caller then deletes the key.
    public func unpair() async throws {
        let (data, response) = try await performSigned(method: "DELETE", segments: ["clients", "me"])
        try check(response, data: data, accept: [200, 204])
    }

    /// `POST /clients/me/connect` (M10): lifts a disconnection set from the
    /// console. Only on an explicit user action ("Reconnect", or opening
    /// the console from the list), never from an automatic reconnection.
    public func connectClient() async throws {
        let (data, response) = try await performSigned(method: "POST", segments: ["clients", "me", "connect"])
        try check(response, data: data, accept: [200, 204])
    }

    // MARK: - Library

    public func systems() async throws -> [GameSystem] {
        let list: SystemList = try await signedJSON(method: "GET", segments: ["systems"])
        return list.systems
    }

    public func games(systemId: String, folderId: String? = nil) async throws -> GameListing {
        var query: [(name: String, value: String)] = []
        if let folderId { query.append((name: "folderId", value: folderId)) }
        return try await signedJSON(method: "GET", segments: ["systems", systemId, "games"], query: query)
    }

    public func game(id: String) async throws -> GameDetail {
        try await signedJSON(method: "GET", segments: ["games", id])
    }

    /// How long `GET /games` may stay silent: the pak lists every system
    /// (up to 20 000 games) before answering.
    public static let allGamesTimeout: TimeInterval = 60

    /// `GET /games` (M11): every game of every system, in one flat list.
    /// An older pak without the route answers `404`.
    public func allGames() async throws -> AllGames {
        try await signedJSON(method: "GET", segments: ["games"], timeout: Self.allGamesTimeout)
    }

    /// `GET /games/{gameId}/files/{fileId}` (M11): one file of the game,
    /// streamed to disk (never held in memory) into a new directory under
    /// `directory`, named after `Content-Disposition`. `progress` gets the
    /// bytes received so far and the expected total (`Content-Length`),
    /// when known. A file no longer in the game's list gets `404 not_found`.
    public func downloadGameFile(
        gameId: String, fileId: String, into directory: URL,
        progress: (@Sendable (Int64, Int64?) -> Void)? = nil
    ) async throws -> DownloadedFile {
        try await downloadSigned(
            segments: ["games", gameId, "files", fileId], fallbackName: "download.bin", into: directory, progress: progress
        )
    }

    /// Largest art accepted from the pak (the pak computes ETags up to
    /// this size, and serves larger files whole): a larger body is never
    /// held in memory, the art is treated as unreadable.
    public static let maxArtBytes = 8 << 20

    /// `GET /games/{gameId}/art`, with `If-None-Match` when an ETag is known.
    /// The body is read up to ``maxArtBytes`` (`Content-Length` or not).
    public func art(gameId: String, ifNoneMatch etag: String? = nil) async throws -> ArtResponse {
        var headers: [String: String] = [:]
        if let etag { headers["If-None-Match"] = etag }
        let (data, response) = try await withSignedRequests(
            method: "GET", segments: ["games", gameId, "art"], query: [], headers: headers,
            body: nil, bodyFile: nil, bodyFileSHA256: nil
        ) { request in
            let (data, response) = try await self.transportLimited(request, maxBytes: Self.maxArtBytes)
            return (data, response, data)
        }
        switch response.statusCode {
        case 304: return .notModified
        case 404: return .none
        default:
            try check(response, data: data, accept: [200])
            return .image(data, etag: response.value(forHTTPHeaderField: "ETag"))
        }
    }

    /// `PUT /games/{gameId}/art` (M8): `png` comes from ``ArtImage``. The
    /// body is signed with its SHA-256. Returns the game as listed
    /// (`hasArt` true). The previous art goes to the trash of the console.
    public func setArt(gameId: String, png: Data) async throws -> GameSummary {
        guard png.count <= ArtImage.maxBytes else {
            throw APIError.server(ServerError(status: 413, code: .tooLarge, message: "Art larger than 4 MiB"))
        }
        return try await signedJSON(
            method: "PUT", segments: ["games", gameId, "art"], headers: ["Content-Type": "image/png"], body: png
        )
    }

    /// `DELETE /games/{gameId}/art` (M8): the art goes to the trash of the
    /// console. Returns the game as listed (`hasArt` false).
    public func deleteArt(gameId: String) async throws -> GameSummary {
        try await signedJSON(method: "DELETE", segments: ["games", gameId, "art"])
    }

    // MARK: - Trash

    public func trash() async throws -> TrashList {
        try await signedJSON(method: "GET", segments: ["trash"])
    }

    /// Permanently empties the trash (`X-RL-Confirm: empty-trash`).
    public func emptyTrash() async throws -> FreedBytes {
        try await signedJSON(method: "DELETE", segments: ["trash"], headers: ["X-RL-Confirm": "empty-trash"])
    }

    /// Permanently deletes one trash entry (`X-RL-Confirm: empty-trash`).
    public func deleteTrashEntry(id: String) async throws -> FreedBytes {
        try await signedJSON(method: "DELETE", segments: ["trash", id], headers: ["X-RL-Confirm": "empty-trash"])
    }

    // MARK: - Library changes

    /// `GET /games/{gameId}/deletion` (M8): what ``deleteGame(id:keepSave:)``
    /// would move and keep now. Read only.
    public func deletionPreview(gameId: String, keepSave: Bool) async throws -> DeletionPreview {
        try await signedJSON(
            method: "GET", segments: ["games", gameId, "deletion"],
            query: [(name: "keepSave", value: keepSave ? "true" : "false")]
        )
    }

    /// `DELETE /games/{gameId}`: moves the game and its data to the trash
    /// (with `keepSave`, the save and the states stay in place).
    public func deleteGame(id: String, keepSave: Bool) async throws -> TrashMoveResult {
        try await signedJSON(
            method: "DELETE", segments: ["games", id],
            query: [(name: "keepSave", value: keepSave ? "true" : "false")]
        )
    }

    // MARK: - Uploads

    /// `POST /uploads`.
    public func createUpload(_ request: CreateUploadRequest) async throws -> UploadCreated {
        try await signedJSON(method: "POST", segments: ["uploads"], body: ProtocolJSON.encoder().encode(request))
    }

    /// `GET /uploads/{uploadId}`: the offset received so far.
    public func uploadStatus(id: String) async throws -> UploadStatus {
        try await signedJSON(method: "GET", segments: ["uploads", id])
    }

    /// `PUT /uploads/{uploadId}`: sends `length` bytes read from `file` (a
    /// file holding exactly this chunk), streamed by `URLSession` from the
    /// disk. The only request signed with `X-RL-Content-SHA256: UNSIGNED`.
    /// Returns the new offset. `onSent` gets the bytes of this chunk sent
    /// so far (not yet confirmed by the device).
    public func sendChunk(
        uploadId: String, offset: Int64, file: URL, length: Int64, onSent: (@Sendable (Int64) -> Void)? = nil
    ) async throws -> Int64 {
        let headers = [
            "Upload-Offset": String(offset),
            "Content-Type": "application/octet-stream",
            "Content-Length": String(length),
        ]
        let (data, response) = try await performSigned(
            method: "PUT", segments: ["uploads", uploadId], headers: headers, bodyFile: file, onSent: onSent
        )
        try check(response, data: data, accept: [200, 204])
        if let value = response.value(forHTTPHeaderField: "Upload-Offset"), let next = Int64(value.trimmingCharacters(in: .whitespaces)) {
            return next
        }
        return offset + length
    }

    /// `POST /uploads/{uploadId}/complete`.
    public func completeUpload(id: String) async throws -> UploadResult {
        try await signedJSON(method: "POST", segments: ["uploads", id, "complete"])
    }

    /// `DELETE /uploads/{uploadId}`.
    public func cancelUpload(id: String) async throws {
        let (data, response) = try await performSigned(method: "DELETE", segments: ["uploads", id])
        try check(response, data: data, accept: [200, 204])
    }

    // MARK: - Downloads and jobs

    /// `POST /downloads`: the device downloads the URL in a job.
    public func createDownload(_ request: DownloadRequest) async throws -> JobAccepted {
        try await signedJSON(method: "POST", segments: ["downloads"], body: ProtocolJSON.encoder().encode(request))
    }

    public func jobs() async throws -> [Job] {
        let list: JobList = try await signedJSON(method: "GET", segments: ["jobs"])
        return list.jobs
    }

    public func job(id: String) async throws -> Job {
        try await signedJSON(method: "GET", segments: ["jobs", id])
    }

    /// `DELETE /jobs/{jobId}`: returns the job in its new state.
    public func cancelJob(id: String) async throws -> Job {
        try await signedJSON(method: "DELETE", segments: ["jobs", id])
    }

    /// `POST /games/{gameId}/patch`: the device applies the patch in a job
    /// (milestone M6). A failed job carries `422 patch_mismatch` or
    /// `422 unsupported_format` in its `error`.
    public func patchGame(gameId: String, _ request: PatchRequest) async throws -> JobAccepted {
        try await signedJSON(method: "POST", segments: ["games", gameId, "patch"], body: ProtocolJSON.encoder().encode(request))
    }

    // MARK: - Saves (milestone M5)

    /// Largest body of `PUT /games/{gameId}/save`; larger saves go through
    /// `POST /uploads` with `target.kind = "save"`.
    public static let maxSaveBodyBytes: Int64 = 64 << 20

    /// `GET /games/{gameId}/save`, written to a new directory under `directory`.
    public func downloadSave(gameId: String, into directory: URL) async throws -> DownloadedFile {
        try await downloadSigned(segments: ["games", gameId, "save"], fallbackName: "save.sav", into: directory)
    }

    /// `GET /games/{gameId}/save/rtc` (M9): the clock file of the save,
    /// written to a new directory under `directory`.
    public func downloadSaveClock(gameId: String, into directory: URL) async throws -> DownloadedFile {
        try await downloadSigned(segments: ["games", gameId, "save", "rtc"], fallbackName: "save.rtc", into: directory)
    }

    /// How long `GET /saves` may stay silent: the pak answers only once
    /// it has listed (and, the first time, hashed) every save.
    public static let savesTimeout: TimeInterval = 180

    /// `GET /saves` (M9): every save of the console, with its SHA-256.
    public func saves() async throws -> SaveList {
        try await signedJSON(method: "GET", segments: ["saves"], timeout: Self.savesTimeout)
    }

    /// `PUT /games/{gameId}/save`: the body is streamed from `file` and
    /// signed with its SHA-256 (`sha256`, computed while reading the file;
    /// never `UNSIGNED`). Each attempt is signed again with a new nonce.
    /// Returns the copy of the previous save (nil if there was none).
    public func replaceSave(gameId: String, file: URL, sha256: String) async throws -> BackupResult {
        let size = try FileDigest.size(of: file)
        guard size <= Self.maxSaveBodyBytes else {
            throw APIError.server(ServerError(status: 413, code: .tooLarge, message: "Save larger than 64 MiB"))
        }
        let headers = ["Content-Type": "application/octet-stream", "Content-Length": String(size)]
        let (data, response) = try await performSigned(
            method: "PUT", segments: ["games", gameId, "save"], headers: headers, bodyFile: file, bodyFileSHA256: sha256
        )
        try check(response, data: data, accept: [200])
        return try decode(data)
    }

    public func saveBackups(gameId: String) async throws -> [SaveBackup] {
        let list: BackupList = try await signedJSON(method: "GET", segments: ["games", gameId, "save", "backups"])
        return list.backups
    }

    /// `POST /games/{gameId}/save/backups` ("duplicate the save").
    public func createSaveBackup(gameId: String, label: String?) async throws -> SaveBackup {
        try await signedJSON(
            method: "POST", segments: ["games", gameId, "save", "backups"],
            body: ProtocolJSON.encoder().encode(CreateBackupRequest(label: label.flatMap(BackupLabel.normalized)))
        )
    }

    /// `POST .../backups/{backupId}/restore`: the current save is copied
    /// first; returns that copy (nil if there was no save).
    public func restoreSaveBackup(gameId: String, backupId: String) async throws -> BackupResult {
        try await signedJSON(method: "POST", segments: ["games", gameId, "save", "backups", backupId, "restore"])
    }

    public func downloadSaveBackup(gameId: String, backupId: String, into directory: URL) async throws -> DownloadedFile {
        try await downloadSigned(segments: ["games", gameId, "save", "backups", backupId], fallbackName: "\(backupId).sav", into: directory)
    }

    public func deleteSaveBackup(gameId: String, backupId: String) async throws {
        let (data, response) = try await performSigned(method: "DELETE", segments: ["games", gameId, "save", "backups", backupId])
        try check(response, data: data, accept: [200, 204])
    }

    // MARK: - States (milestone M5)

    public func states(gameId: String) async throws -> SlotList {
        try await signedJSON(method: "GET", segments: ["games", gameId, "states"])
    }

    public func downloadState(gameId: String, slot: Int, into directory: URL) async throws -> DownloadedFile {
        try await downloadSigned(segments: ["games", gameId, "states", String(slot)], fallbackName: "state\(slot).st", into: directory)
    }

    /// `DELETE /games/{gameId}/states/{slot}`: to the trash. A locked slot
    /// gives `409 state_locked`.
    public func deleteState(gameId: String, slot: Int) async throws -> TrashMoveResult {
        try await signedJSON(method: "DELETE", segments: ["games", gameId, "states", String(slot)])
    }

    /// `GET .../states/{slot}/thumbnail`: PNG data, nil when there is none.
    public func stateThumbnail(gameId: String, slot: Int) async throws -> Data? {
        let (data, response) = try await performSigned(method: "GET", segments: ["games", gameId, "states", String(slot), "thumbnail"])
        if response.statusCode == 404 { return nil }
        try check(response, data: data, accept: [200])
        return data
    }

    public func lockState(gameId: String, slot: Int) async throws -> StateSlot {
        try await signedJSON(method: "PUT", segments: ["games", gameId, "states", String(slot), "lock"])
    }

    public func unlockState(gameId: String, slot: Int) async throws -> StateSlot {
        try await signedJSON(method: "DELETE", segments: ["games", gameId, "states", String(slot), "lock"])
    }

    /// `POST .../states/{slot}/restore`: the locked copy replaces the state
    /// (the current state goes to the trash first).
    public func restoreLockedState(gameId: String, slot: Int) async throws -> StateSlot {
        try await signedJSON(method: "POST", segments: ["games", gameId, "states", String(slot), "restore"])
    }

    public func resumeHistory(gameId: String) async throws -> ResumeHistory {
        try await signedJSON(method: "GET", segments: ["games", gameId, "resume-history"])
    }

    /// `POST .../resume-history/{entryId}/restore`: the current version is
    /// archived first. Returns the updated history.
    public func restoreResumeHistory(gameId: String, entryId: String) async throws -> ResumeHistory {
        try await signedJSON(method: "POST", segments: ["games", gameId, "resume-history", entryId, "restore"])
    }

    // MARK: - Cheats (milestone M5)

    public func cheats(gameId: String) async throws -> CheatList {
        try await signedJSON(method: "GET", segments: ["games", gameId, "cheats"])
    }

    /// `PUT /games/{gameId}/cheats` with `If-Match`. If the file changed in
    /// the meantime, throws a `412` server error (``CheatEditor/isConflict(_:)``).
    public func replaceCheats(gameId: String, _ update: CheatsUpdate, ifMatch etag: String) async throws -> CheatList {
        try await signedJSON(
            method: "PUT", segments: ["games", gameId, "cheats"], headers: ["If-Match": etag],
            body: ProtocolJSON.encoder().encode(update)
        )
    }

    /// `POST /games/{gameId}/cheats/import`. `422 unsupported_format` when
    /// no cheat is found.
    public func importCheats(gameId: String, _ request: CheatImportRequest) async throws -> CheatImportResult {
        try await signedJSON(method: "POST", segments: ["games", gameId, "cheats", "import"], body: ProtocolJSON.encoder().encode(request))
    }

    /// `GET /sd/files?type=...`.
    public func sdFiles(type: SdFileType) async throws -> [SdFile] {
        let list: SdFileList = try await signedJSON(method: "GET", segments: ["sd", "files"], query: [(name: "type", value: type.rawValue)])
        return list.files
    }

    // MARK: - Plumbing

    /// Builds a request. Segments are percent-encoded one by one, and the
    /// query is built with ``Signer/encodeQuery(_:)``, so it is sent already
    /// in its canonical form.
    func makeRequest(
        method: String,
        segments: [String],
        query: [(name: String, value: String)] = []
    ) throws -> URLRequest {
        let names = query.map(\.name)
        precondition(Set(names).count == names.count, "duplicate query key")
        var string = "\(scheme)://\(endpoint.authority)\(Self.apiPrefix)/"
            + segments.map { Signer.encodeRFC3986($0) }.joined(separator: "/")
        if !query.isEmpty {
            string += "?" + Signer.encodeQuery(query)
        }
        guard let url = URL(string: string) else { throw APIError.signing(.missingURL) }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        if let platform { request.setValue(platform.rawValue, forHTTPHeaderField: RequestPlatform.header) }
        return request
    }

    /// Unix time to sign with, corrected by the measured offset.
    func signingTime() -> Int64 {
        Int64((now().timeIntervalSince1970 + clockOffset).rounded(.down))
    }

    /// `timeout`: the longest silence before the request fails, when the
    /// route needs more than the session's (`timeoutIntervalForRequest`).
    private func signedJSON<T: Decodable>(
        method: String,
        segments: [String],
        query: [(name: String, value: String)] = [],
        headers: [String: String] = [:],
        body: Data? = nil,
        timeout: TimeInterval? = nil
    ) async throws -> T {
        let (data, response) = try await performSigned(
            method: method, segments: segments, query: query, headers: headers, body: body, timeout: timeout
        )
        try check(response, data: data, accept: [200, 201, 202])
        return try decode(data)
    }

    /// Signs and sends a request; after `401 clock_skew`, corrects the clock
    /// with `/hello` and signs again (new nonce), once. With `bodyFile`, the
    /// body is streamed from that file and signed with `bodyFileSHA256`, or
    /// as `UNSIGNED` without it (upload chunks only).
    private func performSigned(
        method: String,
        segments: [String],
        query: [(name: String, value: String)] = [],
        headers: [String: String] = [:],
        body: Data? = nil,
        bodyFile: URL? = nil,
        bodyFileSHA256: String? = nil,
        timeout: TimeInterval? = nil,
        onSent: (@Sendable (Int64) -> Void)? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        try await withSignedRequests(method: method, segments: segments, query: query, headers: headers,
                                     body: body, bodyFile: bodyFile, bodyFileSHA256: bodyFileSHA256,
                                     timeout: timeout) { request in
            let (data, response) = try await self.transport(request, bodyFile: bodyFile, onSent: onSent)
            return (data, response, data)
        }
    }

    /// The signing loop shared by every signed route: `send` performs one
    /// signed attempt and returns its result, the response and the body to
    /// read an error from.
    private func withSignedRequests<R: Sendable>(
        method: String,
        segments: [String],
        query: [(name: String, value: String)],
        headers: [String: String],
        body: Data?,
        bodyFile: URL?,
        bodyFileSHA256: String?,
        timeout: TimeInterval? = nil,
        send: (URLRequest) async throws -> (R, HTTPURLResponse, Data)
    ) async throws -> (R, HTTPURLResponse) {
        guard let credentials else { throw APIError.notPaired }
        let signer = Signer(identity: credentials.identity)
        var corrected = false
        while true {
            var request = try makeRequest(method: method, segments: segments, query: query)
            if let timeout { request.timeoutInterval = timeout }
            for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
            if let body {
                request.httpBody = body
                if headers["Content-Type"] == nil {
                    request.setValue("application/json", forHTTPHeaderField: "Content-Type")
                }
            }
            do {
                request = try signer.sign(
                    request, time: signingTime(),
                    unsignedBody: bodyFile != nil && bodyFileSHA256 == nil,
                    bodySHA256: bodyFile != nil ? bodyFileSHA256 : nil
                )
            } catch let error as SignerError {
                throw APIError.signing(error)
            }
            let sentAt = ContinuousClock.now
            let (result, response, errorBody) = try await send(request)
            let serverError = Self.serverError(response, data: errorBody)
            if !corrected, response.statusCode == 401, serverError?.code == .clockSkew {
                corrected = true
                try await hello()
                continue
            }
            if let serverError { errorObserver?(serverError, sentAt) }
            return (result, response)
        }
    }

    /// A signed `GET` whose body is written to a file (never held in
    /// memory), moved into a new directory under `directory`, named after
    /// `Content-Disposition` (or `fallbackName`).
    private func downloadSigned(
        segments: [String], fallbackName: String, into directory: URL,
        progress: (@Sendable (Int64, Int64?) -> Void)? = nil
    ) async throws -> DownloadedFile {
        let ((location, errorBody), response) = try await withSignedRequests(
            method: "GET", segments: segments, query: [], headers: [:], body: nil, bodyFile: nil, bodyFileSHA256: nil
        ) { request -> ((URL?, Data), HTTPURLResponse, Data) in
            let (location, response) = try await self.transportDownload(request, progress: progress)
            guard response.statusCode != 200 else { return ((location, Data()), response, Data()) }
            // Not the file: its body is an error, small. Read it (the error
            // is decoded from it), then drop the file.
            let errorBody = Self.readSmall(location)
            try? FileManager.default.removeItem(at: location)
            return ((nil, errorBody), response, errorBody)
        }
        guard response.statusCode == 200, let location else {
            try check(response, data: errorBody, accept: [200])
            throw APIError.unexpectedStatus(response.statusCode)
        }
        let name = ContentDisposition.fileName(response.value(forHTTPHeaderField: "Content-Disposition"))
            ?? ContentDisposition.safe(fallbackName) ?? "download.bin"
        let folder = directory.appending(path: UUID().uuidString, directoryHint: .isDirectory)
        do {
            try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
            let destination = folder.appending(path: name)
            try FileManager.default.moveItem(at: location, to: destination)
            return DownloadedFile(url: destination)
        } catch {
            try? FileManager.default.removeItem(at: location)
            try? FileManager.default.removeItem(at: folder)
            throw error
        }
    }

    /// At most 64 KiB of a file (an error body).
    static func readSmall(_ url: URL) -> Data {
        guard let handle = try? FileHandle(forReadingFrom: url) else { return Data() }
        defer { try? handle.close() }
        return (try? handle.read(upToCount: 64 << 10)) ?? Data()
    }

    private func transportDownload(
        _ request: URLRequest, progress: (@Sendable (Int64, Int64?) -> Void)? = nil
    ) async throws -> (URL, HTTPURLResponse) {
        let trust = PinnedTrust(pin: pin)
        do {
            let (location, response) = if let progress {
                try await PakDownloadDelegate.download(request, session: session, trust: trust, onReceived: progress)
            } else {
                try await session.download(for: request, delegate: PakTaskDelegate(trust: trust))
            }
            guard let http = response as? HTTPURLResponse else {
                try? FileManager.default.removeItem(at: location)
                throw APIError.invalidResponse("not an HTTP response")
            }
            // The session may delete its file: keep our own.
            let kept = FileManager.default.temporaryDirectory.appending(path: "rl-dl-\(UUID().uuidString)")
            try FileManager.default.moveItem(at: location, to: kept)
            return (kept, http)
        } catch let error as URLError {
            throw PakTaskDelegate.failure(error, trust: trust)
        }
    }

    /// Sends one request. Redirections are never followed (see
    /// ``PakTaskDelegate``); `onSent` gets the body bytes sent so far.
    private func transport(
        _ request: URLRequest, bodyFile: URL? = nil, onSent: (@Sendable (Int64) -> Void)? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        let delegate = PakTaskDelegate(pin: pin, onSent: onSent)
        do {
            let (data, response) = if let bodyFile {
                try await session.upload(for: request, fromFile: bodyFile, delegate: delegate)
            } else {
                try await session.data(for: request, delegate: delegate)
            }
            guard let http = response as? HTTPURLResponse else {
                delegate.finish(totalBytesSent: nil)
                throw APIError.invalidResponse("not an HTTP response")
            }
            // The send reports reach the delegate asynchronously: close them
            // before returning so that a caller sees all of them, in order,
            // ending with the whole body once the pak accepted it.
            if let bodyFile, (200..<300).contains(http.statusCode) {
                let size = (try? bodyFile.resourceValues(forKeys: [.fileSizeKey]).fileSize).map(Int64.init)
                delegate.finish(totalBytesSent: size)
            } else {
                delegate.finish(totalBytesSent: nil)
            }
            return (data, http)
        } catch let error as URLError {
            delegate.finish(totalBytesSent: nil)
            throw delegate.failure(error)
        } catch {
            delegate.finish(totalBytesSent: nil)
            throw error
        }
    }

    /// Sends a request and reads at most `maxBytes` of its body; a larger
    /// body (announced or not) stops the request.
    private func transportLimited(_ request: URLRequest, maxBytes: Int) async throws -> (Data, HTTPURLResponse) {
        let tooLarge = APIError.invalidResponse("body larger than \(maxBytes) bytes")
        let delegate = PakTaskDelegate(pin: pin)
        do {
            let (bytes, response) = try await session.bytes(for: request, delegate: delegate)
            guard let http = response as? HTTPURLResponse else {
                bytes.task.cancel()
                throw APIError.invalidResponse("not an HTTP response")
            }
            if http.expectedContentLength > Int64(maxBytes) {
                bytes.task.cancel()
                throw tooLarge
            }
            var data = Data()
            if http.expectedContentLength > 0 { data.reserveCapacity(Int(http.expectedContentLength)) }
            var buffer: [UInt8] = []
            buffer.reserveCapacity(64 << 10)
            for try await byte in bytes {
                buffer.append(byte)
                if buffer.count == 64 << 10 {
                    guard data.count + buffer.count <= maxBytes else {
                        bytes.task.cancel()
                        throw tooLarge
                    }
                    data.append(contentsOf: buffer)
                    buffer.removeAll(keepingCapacity: true)
                }
            }
            guard data.count + buffer.count <= maxBytes else { throw tooLarge }
            data.append(contentsOf: buffer)
            return (data, http)
        } catch let error as URLError {
            throw delegate.failure(error)
        }
    }

    private func check(_ response: HTTPURLResponse, data: Data, accept: Set<Int>) throws {
        if accept.contains(response.statusCode) { return }
        if let error = Self.serverError(response, data: data) { throw APIError.server(error) }
        throw APIError.unexpectedStatus(response.statusCode)
    }

    private func decode<T: Decodable>(_ data: Data) throws -> T {
        do {
            return try ProtocolJSON.decoder().decode(T.self, from: data)
        } catch {
            throw APIError.invalidResponse(String(describing: error))
        }
    }

    /// Decodes an error body; nil for a success status or an unreadable body.
    static func serverError(_ response: HTTPURLResponse, data: Data) -> ServerError? {
        guard response.statusCode >= 400,
              let envelope = try? JSONDecoder().decode(ErrorEnvelope.self, from: data)
        else { return nil }
        let retryAfter = response.value(forHTTPHeaderField: "Retry-After").flatMap { Int($0.trimmingCharacters(in: .whitespaces)) }
        return ServerError(
            status: response.statusCode,
            code: ErrorCode(rawValue: envelope.error.code),
            message: envelope.error.message,
            details: envelope.error.details ?? [:],
            retryAfter: retryAfter
        )
    }
}

/// Task delegate of every request to the pak.
///
/// - Redirections are refused: the `3xx` answer is returned as it is. A
///   redirection would send the request again outside the signing loop
///   (without a new nonce), possibly with its body, and possibly off the
///   local network.
/// - `onSent` reports the body bytes sent so far (upload chunks): growing
///   only, and never after ``finish(totalBytesSent:)``.
/// - Server trust (M13): only the pinned certificate key is accepted
///   (``PinnedTrust``), never the system trust. The session has no
///   delegate of its own, so URLSession hands its session-level challenges
///   (server trust included) to this task delegate.
class PakTaskDelegate: NSObject, URLSessionTaskDelegate, @unchecked Sendable {
    let trust: PinnedTrust
    private let onSent: (@Sendable (Int64) -> Void)?
    private let lock = NSLock()
    private var reported: Int64 = 0
    private var finished = false

    init(trust: PinnedTrust, onSent: (@Sendable (Int64) -> Void)? = nil) {
        self.trust = trust
        self.onSent = onSent
    }

    convenience init(pin: String?, onSent: (@Sendable (Int64) -> Void)? = nil) {
        self.init(trust: PinnedTrust(pin: pin), onSent: onSent)
    }

    func urlSession(
        _ session: URLSession, task: URLSessionTask, didReceive challenge: URLAuthenticationChallenge,
        completionHandler: @escaping @Sendable (URLSession.AuthChallengeDisposition, URLCredential?) -> Void
    ) {
        let (disposition, credential) = trust.evaluate(challenge)
        completionHandler(disposition, credential)
    }

    /// The error a failed request throws: ``APIError/identityChanged`` when
    /// the console's certificate was refused, a cancellation, or a network
    /// error.
    func failure(_ error: URLError) -> Error {
        Self.failure(error, trust: trust)
    }

    static func failure(_ error: URLError, trust: PinnedTrust) -> Error {
        if trust.refused { return APIError.identityChanged }
        if error.code == .cancelled { return CancellationError() }
        return APIError.network(error.code)
    }

    /// Closes the reports of the request: a last one with `totalBytesSent`
    /// when it is more than what was reported, then none (a late callback
    /// from the session is dropped).
    func finish(totalBytesSent: Int64?) {
        lock.withLock {
            guard !finished else { return }
            finished = true
            if let totalBytesSent, totalBytesSent > reported {
                reported = totalBytesSent
                onSent?(totalBytesSent)
            }
        }
    }

    func urlSession(
        _ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest
    ) async -> URLRequest? {
        nil
    }

    func urlSession(
        _ session: URLSession, task: URLSessionTask, didSendBodyData bytesSent: Int64,
        totalBytesSent: Int64, totalBytesExpectedToSend: Int64
    ) {
        guard let onSent else { return }
        lock.withLock {
            guard !finished, totalBytesSent > reported else { return }
            reported = totalBytesSent
            onSent(totalBytesSent)
        }
    }
}

/// Task delegate of a download that reports its progress: the bytes
/// written to disk so far, and the expected total when the pak sent
/// `Content-Length`. The async `download(for:delegate:)` does not report
/// them, so the task is driven here. Redirections stay refused.
final class PakDownloadDelegate: PakTaskDelegate, URLSessionDownloadDelegate, @unchecked Sendable {
    private let onReceived: @Sendable (Int64, Int64?) -> Void
    private let lock = NSLock()
    private var continuation: CheckedContinuation<(URL, URLResponse), Error>?
    /// The file, moved out of the session's temporary location.
    private var kept: URL?

    private init(trust: PinnedTrust, onReceived: @escaping @Sendable (Int64, Int64?) -> Void) {
        self.onReceived = onReceived
        super.init(trust: trust, onSent: nil)
    }

    /// Downloads `request` to a file of the app's temporary directory.
    static func download(
        _ request: URLRequest, session: URLSession, trust: PinnedTrust,
        onReceived: @escaping @Sendable (Int64, Int64?) -> Void
    ) async throws -> (URL, URLResponse) {
        let delegate = PakDownloadDelegate(trust: trust, onReceived: onReceived)
        let task = session.downloadTask(with: request)
        task.delegate = delegate
        return try await withTaskCancellationHandler {
            try await withCheckedThrowingContinuation { continuation in
                delegate.lock.withLock { delegate.continuation = continuation }
                if Task.isCancelled {
                    delegate.finish(.failure(URLError(.cancelled)))
                } else {
                    task.resume()
                }
            }
        } onCancel: {
            task.cancel()
        }
    }

    private func finish(_ result: Result<(URL, URLResponse), Error>) {
        let continuation = lock.withLock { () -> CheckedContinuation<(URL, URLResponse), Error>? in
            defer { self.continuation = nil }
            return self.continuation
        }
        continuation?.resume(with: result)
    }

    func urlSession(
        _ session: URLSession, downloadTask: URLSessionDownloadTask, didWriteData bytesWritten: Int64,
        totalBytesWritten: Int64, totalBytesExpectedToWrite: Int64
    ) {
        onReceived(totalBytesWritten, totalBytesExpectedToWrite > 0 ? totalBytesExpectedToWrite : nil)
    }

    /// The session deletes its file when this returns: it is moved first.
    func urlSession(_ session: URLSession, downloadTask: URLSessionDownloadTask, didFinishDownloadingTo location: URL) {
        let destination = FileManager.default.temporaryDirectory.appending(path: "rl-dl-\(UUID().uuidString)")
        if (try? FileManager.default.moveItem(at: location, to: destination)) != nil {
            lock.withLock { kept = destination }
        }
    }

    func urlSession(_ session: URLSession, task: URLSessionTask, didCompleteWithError error: Error?) {
        let kept = lock.withLock { self.kept }
        if let error {
            if let kept { try? FileManager.default.removeItem(at: kept) }
            finish(.failure(error))
        } else if let kept, let response = task.response {
            finish(.success((kept, response)))
        } else {
            finish(.failure(URLError(.cannotCreateFile)))
        }
    }
}

/// Dotted version comparison for `minClientVersion`.
public enum AppVersion {
    /// Whether `version` is at least `minimum` (numeric, component by
    /// component; missing components are 0; a suffix after `-` is ignored).
    public static func isSupported(_ version: String, minimum: String) -> Bool {
        let a = components(version)
        let b = components(minimum)
        for i in 0..<max(a.count, b.count) {
            let x = i < a.count ? a[i] : 0
            let y = i < b.count ? b[i] : 0
            if x != y { return x > y }
        }
        return true
    }

    private static func components(_ version: String) -> [Int] {
        let core = version.split(separator: "-", maxSplits: 1).first.map(String.init) ?? ""
        return core.split(separator: ".").map { Int($0) ?? 0 }
    }
}

/// The bytes of an open event stream, read straight from URLSession
/// (pulled by the parser, never buffered here).
final class EventBytes: EventByteSource, @unchecked Sendable {
    private var iterator: URLSession.AsyncBytes.AsyncIterator
    private let task: URLSessionDataTask

    init(bytes: URLSession.AsyncBytes) {
        iterator = bytes.makeAsyncIterator()
        task = bytes.task
    }

    func next() async throws -> UInt8? {
        do {
            return try await iterator.next()
        } catch let error as URLError {
            throw error.code == .cancelled ? CancellationError() : APIError.network(error.code)
        }
    }

    func cancel() { task.cancel() }
}
