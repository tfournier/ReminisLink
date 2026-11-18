import Foundation

/// The pairing link of the QR code (docs/specs/03-protocole.md §3.1):
///
///     reminislink://pair?v=1&id=<deviceId>&n=<name>&s=<secret b64u>&h=<IPv4>&p=<port>
///
/// `h` and `p` only serve the current session when Bonjour fails: the app
/// never stores them.
public struct PairingLink: Sendable, Equatable {
    public static let scheme = "reminislink"
    public static let host = "pair"

    public let deviceId: String
    public let deviceName: String?
    /// The 32-byte pairing secret.
    public let secret: Data
    /// Fallback endpoint for this session only, when both `h` and `p` are valid.
    public let fallbackEndpoint: Endpoint?

    public init(deviceId: String, deviceName: String?, secret: Data, fallbackEndpoint: Endpoint?) {
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.secret = secret
        self.fallbackEndpoint = fallbackEndpoint
    }

    /// Parses a link. Returns nil unless the scheme, the version, the device
    /// id and the secret are all valid. A repeated parameter is refused.
    public init?(url: URL) {
        guard url.scheme?.lowercased() == Self.scheme,
              url.host(percentEncoded: true)?.lowercased() == Self.host,
              url.user == nil, url.password == nil, url.port == nil, url.fragment == nil,
              url.path(percentEncoded: true).isEmpty || url.path(percentEncoded: true) == "/",
              let rawQuery = url.query(percentEncoded: true),
              let params = Self.parseQuery(rawQuery)
        else { return nil }
        guard params["v"] == "1",
              let id = params["id"], Self.isLowercaseUUID(id),
              let s = params["s"], let secret = Base64URL.decode(s), secret.count == PairingSecret.secretSize
        else { return nil }
        let name = params["n"].map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.flatMap { $0.isEmpty ? nil : $0 }
        var endpoint: Endpoint?
        // Only a private or link-local IPv4: a link must not send the pairing
        // exchange to an address outside the local network.
        if let h = params["h"], Self.isLocalIPv4(h), let p = params["p"], let port = UInt16(p), port > 0 {
            endpoint = Endpoint(host: h, port: port)
        }
        self.init(deviceId: id, deviceName: name, secret: secret, fallbackEndpoint: endpoint)
    }

    public init?(string: String) {
        guard let url = URL(string: string.trimmingCharacters(in: .whitespacesAndNewlines), encodingInvalidCharacters: false) else { return nil }
        self.init(url: url)
    }

    /// The link, as the pak encodes it (for tests and the demo mode).
    public var url: URL {
        var items: [(name: String, value: String)] = [
            (name: "v", value: "1"), (name: "id", value: deviceId), (name: "s", value: Base64URL.encode(secret)),
        ]
        if let deviceName { items.append((name: "n", value: deviceName)) }
        if let fallbackEndpoint {
            items.append((name: "h", value: fallbackEndpoint.host))
            items.append((name: "p", value: String(fallbackEndpoint.port)))
        }
        return URL(string: "\(Self.scheme)://\(Self.host)?" + Signer.encodeQuery(items))!
    }

    /// Form-urlencoded query ("+" is a space). Nil on a malformed escape or
    /// a repeated key.
    static func parseQuery(_ raw: String) -> [String: String]? {
        var result: [String: String] = [:]
        for part in raw.split(separator: "&", omittingEmptySubsequences: true) {
            let pieces = part.split(separator: "=", maxSplits: 1, omittingEmptySubsequences: false)
            guard let key = decode(pieces[0]) else { return nil }
            guard let value = decode(pieces.count > 1 ? pieces[1] : "") else { return nil }
            guard result[key] == nil else { return nil }
            result[key] = value
        }
        return result
    }

    /// Strict form decoding: "+" is a space, "%" must start two hex digits,
    /// and the result must be valid UTF-8.
    private static func decode(_ s: Substring) -> String? {
        let bytes = Array(s.utf8)
        var out: [UInt8] = []
        var i = 0
        while i < bytes.count {
            switch bytes[i] {
            case UInt8(ascii: "+"):
                out.append(UInt8(ascii: " "))
            case UInt8(ascii: "%"):
                guard i + 2 < bytes.count, let hi = hex(bytes[i + 1]), let lo = hex(bytes[i + 2]) else { return nil }
                out.append(hi << 4 | lo)
                i += 2
            default:
                out.append(bytes[i])
            }
            i += 1
        }
        return String(bytes: out, encoding: .utf8)
    }

    private static func hex(_ c: UInt8) -> UInt8? {
        switch c {
        case UInt8(ascii: "0")...UInt8(ascii: "9"): c - UInt8(ascii: "0")
        case UInt8(ascii: "a")...UInt8(ascii: "f"): c - UInt8(ascii: "a") + 10
        case UInt8(ascii: "A")...UInt8(ascii: "F"): c - UInt8(ascii: "A") + 10
        default: nil
        }
    }

    static func isLowercaseUUID(_ s: String) -> Bool {
        s.range(of: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", options: .regularExpression) != nil
    }

    /// 10/8, 172.16/12, 192.168/16 (private) or 169.254/16 (link-local).
    public static func isLocalIPv4(_ s: String) -> Bool {
        guard isIPv4(s) else { return false }
        let b = s.split(separator: ".").compactMap { Int($0) }
        switch (b[0], b[1]) {
        case (10, _), (192, 168), (169, 254): return true
        case (172, 16...31): return true
        default: return false
        }
    }

    static func isIPv4(_ s: String) -> Bool {
        let parts = s.split(separator: ".", omittingEmptySubsequences: false)
        return parts.count == 4 && parts.allSatisfy { p in
            !p.isEmpty && p.count <= 3 && p.allSatisfy(\.isASCIIDigit) && (Int(p) ?? 256) <= 255
                && !(p.count > 1 && p.first == "0")
        }
    }
}

extension Character {
    fileprivate var isASCIIDigit: Bool { isASCII && isNumber }
}
