// Debug builds only: the fake pak never ships in a release build.
#if DEBUG
import CryptoKit
import Foundation
import ReminisLinkKit

/// An in-memory pak that follows protocol/openapi.yaml for the routes the
/// app uses so far: `/hello`, `/pair`, signed `/device`, `/systems`,
/// `/systems/{id}/games`, `/games/{id}`, `/games/{id}/art`, `/trash` and
/// `DELETE /clients/me`.
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
    public let library: DemoLibrary
    public let minClientVersion: String
    public let apiVersion: Int

    private let lock = NSLock()
    private var _clockOffset: TimeInterval = 0
    private var _responseDelay: TimeInterval = 0
    private var _pairDelay: TimeInterval = 0
    private var clients: [String: SymmetricKey] = [:]
    private var nonces = Set<String>()
    private var _log: [LoggedRequest] = []
    private var queued: [FakeResponse] = []
    private var trashEntries: [[String: Any]]

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
        self.library = library
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

    /// The link of the QR code. The demo host never resolves, so the link
    /// carries no `h`/`p`: the demo discovery provides the endpoint.
    public var pairingLink: PairingLink {
        PairingLink(deviceId: deviceId, deviceName: deviceName, secret: secret, fallbackEndpoint: nil)
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

    public var clientIds: [String] { lock.withLock { clients.keys.sorted() } }

    /// Registers a client directly, as if it had paired.
    public func addClient(id: String, key: SymmetricKey) {
        lock.withLock { clients[id] = key }
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
            lock.withLock { _ = clients.removeValue(forKey: clientId) }
            return FakeResponse(status: 204)
        case ("GET", ["systems"]):
            return systems()
        case ("GET", let s) where s.count == 3 && s[0] == "systems" && s[2] == "games":
            return games(systemId: s[1], folderId: params["folderId"])
        case ("GET", let s) where s.count == 2 && s[0] == "games":
            return game(id: s[1])
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
                return total
            }
            return .json(200, ["freedBytes": freed])
        case ("DELETE", let s) where s.count == 2 && s[0] == "trash":
            guard request.value(forHTTPHeaderField: "X-RL-Confirm") == "empty-trash" else {
                return .error(400, code: "invalid_request", message: "Missing confirmation")
            }
            let freed = lock.withLock { () -> Int64? in
                guard let index = trashEntries.firstIndex(where: { $0["id"] as? String == s[1] }) else { return nil }
                return Self.size(trashEntries.remove(at: index)["sizeBytes"])
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
        lock.withLock { _ = nonces.insert(clientId + "|" + nonce) }
        let keys = Self.decodeQuery(query).map(\.0)
        guard Set(keys).count == keys.count else {
            return .error(400, code: "invalid_request", message: "Duplicate query key")
        }
        guard content != Signer.unsignedPayload, content == Signer.contentSHA256(body) else {
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
        addClient(id: request.clientId, key: key)
        if pairDelay > 0 { Thread.sleep(forTimeInterval: pairDelay) }
        return .json(200, [
            "deviceId": deviceId, "deviceName": deviceName,
            "deviceNonce": Base64URL.encode(deviceNonce),
            "proof": Pairing.deviceProof(key: key, deviceId: deviceId, clientId: request.clientId),
        ])
    }

    private func device() -> FakeResponse {
        .json(200, [
            "deviceId": deviceId, "deviceName": deviceName, "model": model.rawValue,
            "platform": "tg5040", "os": "nextui", "osVersion": "6.14.0", "pakVersion": "1.0.0",
            "network": ["ipv4": "192.168.1.42", "ssid": "DemoNet"],
            "battery": ["percent": 78, "charging": false],
            "storage": [["id": "sd", "label": "SD", "totalBytes": 127_865_454_592, "freeBytes": 40_265_318_400]],
            "uptimeSeconds": 512,
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
        let total = files.reduce(Int64(0)) { $0 + Self.size($1["sizeBytes"]) }
        return .json(200, [
            "id": id, "systemId": system.id, "name": game.name, "fileName": game.fileName,
            "kind": game.kind.rawValue, "files": files, "totalBytes": total,
        ])
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
#endif
