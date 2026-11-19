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

/// What the app keeps in the Keychain for one paired device.
public struct DeviceCredentials: Sendable {
    public let deviceId: String
    public let clientId: String
    public let key: SymmetricKey

    public init(deviceId: String, clientId: String, key: SymmetricKey) {
        self.deviceId = deviceId
        self.clientId = clientId
        self.key = key
    }
}

/// Result of `GET /games/{gameId}/art`.
public enum ArtResponse: Sendable, Equatable {
    case image(Data, etag: String?)
    case notModified
    case none
}

/// Client of the pak API (protocol/openapi.yaml).
///
/// Every route but `/hello` and `/pair` is signed. A new nonce is drawn for
/// every attempt: after `401 clock_skew`, the client asks `/hello` for the
/// device time, corrects its offset and signs the request again, once.
public actor APIClient {
    public static let apiPrefix = "/api/v1"

    public nonisolated let endpoint: Endpoint
    /// The device this client expects to talk to (checked on `/hello`).
    public nonisolated let expectedDeviceId: String?
    private let credentials: DeviceCredentials?
    private let session: URLSession
    private let now: @Sendable () -> Date
    private let appVersion: String

    /// Device time minus local time, measured on the last `/hello`.
    public private(set) var clockOffset: TimeInterval = 0

    public init(
        endpoint: Endpoint,
        expectedDeviceId: String? = nil,
        credentials: DeviceCredentials? = nil,
        appVersion: String = "1.0.0",
        session: URLSession = APIClient.makeSession(),
        now: @escaping @Sendable () -> Date = { Date() }
    ) {
        self.endpoint = endpoint
        self.expectedDeviceId = expectedDeviceId ?? credentials?.deviceId
        self.credentials = credentials
        self.appVersion = appVersion
        self.session = session
        self.now = now
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
    @discardableResult
    public func hello() async throws -> Hello {
        let request = try makeRequest(method: "GET", segments: ["hello"])
        let (data, response) = try await transport(request)
        try check(response, data: data, accept: [200])
        let hello: Hello = try decode(data)
        clockOffset = hello.serverTime.timeIntervalSince(now())
        if let expected = expectedDeviceId, hello.deviceId != expected {
            throw APIError.deviceMismatch(expected: expected, actual: hello.deviceId)
        }
        guard hello.apiVersion == 1, AppVersion.isSupported(appVersion, minimum: hello.minClientVersion) else {
            throw APIError.incompatible(apiVersion: hello.apiVersion, minClientVersion: hello.minClientVersion)
        }
        return hello
    }

    /// `POST /pair`, then checks the device proof. Returns the shared key,
    /// to store in the Keychain.
    public func pair(_ attempt: PairingAttempt) async throws -> SymmetricKey {
        var request = try makeRequest(method: "POST", segments: ["pair"])
        request.httpBody = try ProtocolJSON.encoder().encode(attempt.request)
        request.setValue("application/json", forHTTPHeaderField: "Content-Type")
        let (data, response) = try await transport(request)
        try check(response, data: data, accept: [200])
        let pairResponse: PairResponse = try decode(data)
        do {
            return try attempt.finish(pairResponse)
        } catch let error as PairingError {
            throw APIError.pairing(error)
        }
    }

    // MARK: - Device

    public func device() async throws -> DeviceStatus {
        try await signedJSON(method: "GET", segments: ["device"])
    }

    /// `DELETE /clients/me`. The caller then deletes the key.
    public func unpair() async throws {
        let (data, response) = try await performSigned(method: "DELETE", segments: ["clients", "me"])
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
    /// Returns the new offset.
    public func sendChunk(uploadId: String, offset: Int64, file: URL, length: Int64) async throws -> Int64 {
        let headers = [
            "Upload-Offset": String(offset),
            "Content-Type": "application/octet-stream",
            "Content-Length": String(length),
        ]
        let (data, response) = try await performSigned(
            method: "PUT", segments: ["uploads", uploadId], headers: headers, bodyFile: file
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
        var string = "http://\(endpoint.authority)\(Self.apiPrefix)/"
            + segments.map { Signer.encodeRFC3986($0) }.joined(separator: "/")
        if !query.isEmpty {
            string += "?" + Signer.encodeQuery(query)
        }
        guard let url = URL(string: string) else { throw APIError.signing(.missingURL) }
        var request = URLRequest(url: url)
        request.httpMethod = method
        request.setValue("application/json", forHTTPHeaderField: "Accept")
        return request
    }

    /// Unix time to sign with, corrected by the measured offset.
    func signingTime() -> Int64 {
        Int64((now().timeIntervalSince1970 + clockOffset).rounded(.down))
    }

    private func signedJSON<T: Decodable>(
        method: String,
        segments: [String],
        query: [(name: String, value: String)] = [],
        headers: [String: String] = [:],
        body: Data? = nil
    ) async throws -> T {
        let (data, response) = try await performSigned(method: method, segments: segments, query: query, headers: headers, body: body)
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
        bodyFileSHA256: String? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        try await withSignedRequests(method: method, segments: segments, query: query, headers: headers,
                                     body: body, bodyFile: bodyFile, bodyFileSHA256: bodyFileSHA256) { request in
            let (data, response) = try await self.transport(request, bodyFile: bodyFile)
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
        send: (URLRequest) async throws -> (R, HTTPURLResponse, Data)
    ) async throws -> (R, HTTPURLResponse) {
        guard let credentials else { throw APIError.notPaired }
        let signer = Signer(clientId: credentials.clientId, key: credentials.key)
        var corrected = false
        while true {
            var request = try makeRequest(method: method, segments: segments, query: query)
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
            let (result, response, errorBody) = try await send(request)
            if !corrected, response.statusCode == 401, Self.serverError(response, data: errorBody)?.code == .clockSkew {
                corrected = true
                try await hello()
                continue
            }
            return (result, response)
        }
    }

    /// A signed `GET` whose body is written to a file (never held in
    /// memory), moved into a new directory under `directory`, named after
    /// `Content-Disposition` (or `fallbackName`).
    private func downloadSigned(segments: [String], fallbackName: String, into directory: URL) async throws -> DownloadedFile {
        let ((location, errorBody), response) = try await withSignedRequests(
            method: "GET", segments: segments, query: [], headers: [:], body: nil, bodyFile: nil, bodyFileSHA256: nil
        ) { request -> ((URL?, Data), HTTPURLResponse, Data) in
            let (location, response) = try await self.transportDownload(request)
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

    private func transportDownload(_ request: URLRequest) async throws -> (URL, HTTPURLResponse) {
        do {
            let (location, response) = try await session.download(for: request, delegate: PakTaskDelegate())
            guard let http = response as? HTTPURLResponse else {
                try? FileManager.default.removeItem(at: location)
                throw APIError.invalidResponse("not an HTTP response")
            }
            // The session may delete its file: keep our own.
            let kept = FileManager.default.temporaryDirectory.appending(path: "rl-dl-\(UUID().uuidString)")
            try FileManager.default.moveItem(at: location, to: kept)
            return (kept, http)
        } catch let error as URLError {
            if error.code == .cancelled { throw CancellationError() }
            throw APIError.network(error.code)
        }
    }

    /// Sends one request. Redirections are never followed (see
    /// ``PakTaskDelegate``); `onSent` gets the body bytes sent so far.
    private func transport(
        _ request: URLRequest, bodyFile: URL? = nil, onSent: (@Sendable (Int64) -> Void)? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        let delegate = PakTaskDelegate(onSent: onSent)
        do {
            let (data, response) = if let bodyFile {
                try await session.upload(for: request, fromFile: bodyFile, delegate: delegate)
            } else {
                try await session.data(for: request, delegate: delegate)
            }
            guard let http = response as? HTTPURLResponse else {
                throw APIError.invalidResponse("not an HTTP response")
            }
            return (data, http)
        } catch let error as URLError {
            if error.code == .cancelled { throw CancellationError() }
            throw APIError.network(error.code)
        }
    }

    /// Sends a request and reads at most `maxBytes` of its body; a larger
    /// body (announced or not) stops the request.
    private func transportLimited(_ request: URLRequest, maxBytes: Int) async throws -> (Data, HTTPURLResponse) {
        let tooLarge = APIError.invalidResponse("body larger than \(maxBytes) bytes")
        do {
            let (bytes, response) = try await session.bytes(for: request, delegate: PakTaskDelegate())
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
            if error.code == .cancelled { throw CancellationError() }
            throw APIError.network(error.code)
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
/// - `onSent` reports the body bytes sent so far (upload chunks).
final class PakTaskDelegate: NSObject, URLSessionTaskDelegate, Sendable {
    private let onSent: (@Sendable (Int64) -> Void)?

    init(onSent: (@Sendable (Int64) -> Void)? = nil) {
        self.onSent = onSent
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
        onSent?(totalBytesSent)
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
