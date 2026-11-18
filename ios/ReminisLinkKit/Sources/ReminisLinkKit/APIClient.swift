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

    /// `GET /games/{gameId}/art`, with `If-None-Match` when an ETag is known.
    public func art(gameId: String, ifNoneMatch etag: String? = nil) async throws -> ArtResponse {
        var headers: [String: String] = [:]
        if let etag { headers["If-None-Match"] = etag }
        let (data, response) = try await performSigned(method: "GET", segments: ["games", gameId, "art"], headers: headers)
        switch response.statusCode {
        case 304: return .notModified
        case 404: return .none
        default:
            try check(response, data: data, accept: [200])
            return .image(data, etag: response.value(forHTTPHeaderField: "ETag"))
        }
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
        headers: [String: String] = [:]
    ) async throws -> T {
        let (data, response) = try await performSigned(method: method, segments: segments, query: query, headers: headers)
        try check(response, data: data, accept: [200, 201])
        return try decode(data)
    }

    /// Signs and sends a request; after `401 clock_skew`, corrects the clock
    /// with `/hello` and signs again (new nonce), once.
    private func performSigned(
        method: String,
        segments: [String],
        query: [(name: String, value: String)] = [],
        headers: [String: String] = [:],
        body: Data? = nil
    ) async throws -> (Data, HTTPURLResponse) {
        guard let credentials else { throw APIError.notPaired }
        let signer = Signer(clientId: credentials.clientId, key: credentials.key)
        var corrected = false
        while true {
            var request = try makeRequest(method: method, segments: segments, query: query)
            for (name, value) in headers { request.setValue(value, forHTTPHeaderField: name) }
            if let body {
                request.httpBody = body
                request.setValue("application/json", forHTTPHeaderField: "Content-Type")
            }
            do {
                request = try signer.sign(request, time: signingTime())
            } catch let error as SignerError {
                throw APIError.signing(error)
            }
            let (data, response) = try await transport(request)
            if !corrected, response.statusCode == 401, Self.serverError(response, data: data)?.code == .clockSkew {
                corrected = true
                try await hello()
                continue
            }
            return (data, response)
        }
    }

    private func transport(_ request: URLRequest) async throws -> (Data, HTTPURLResponse) {
        do {
            let (data, response) = try await session.data(for: request)
            guard let http = response as? HTTPURLResponse else {
                throw APIError.invalidResponse("not an HTTP response")
            }
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
