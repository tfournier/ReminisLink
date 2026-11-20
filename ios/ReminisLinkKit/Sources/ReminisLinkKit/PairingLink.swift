import Foundation

/// Why a pairing link was refused.
public enum PairingLinkError: Error, Equatable, Sendable {
    /// Not a valid ReminisLink pairing link.
    case invalid
    /// A pairing link of an older pak (`v=1`, before M13): the pak must be
    /// updated.
    case outdatedPak
    /// A pairing link of a newer pak than this app knows: the app must be
    /// updated.
    case newerVersion
}

/// The pairing link of the QR code, version 2 (docs/specs/03-protocole.md
/// §3.1, M13):
///
///     reminislink://pair?v=2&id=<deviceId>&n=<name>&s=<secret b64u>&h=<IPv4>&p=<port>&k=<pin>
///
/// `k` is the pin of the console's certificate (b64u of the SHA-256 of its
/// SubjectPublicKeyInfo): it authenticates the console, so the app may
/// connect to `h`:`p` when Bonjour does not find the `id`.
public struct PairingLink: Sendable, Equatable {
    public static let scheme = "reminislink"
    public static let host = "pair"
    public static let version = 2

    public let deviceId: String
    public let deviceName: String?
    /// The 32-byte pairing secret.
    public let secret: Data
    /// `h`:`p`, the address of the console when the QR code was shown.
    public let endpoint: Endpoint?
    /// `k`, the certificate pin (b64u, 32 bytes once decoded).
    public let pin: String

    public init(deviceId: String, deviceName: String?, secret: Data, endpoint: Endpoint?, pin: String) {
        self.deviceId = deviceId
        self.deviceName = deviceName
        self.secret = secret
        self.endpoint = endpoint
        self.pin = pin
    }

    /// Parses a link. A repeated parameter is refused.
    public static func parse(_ url: URL) throws(PairingLinkError) -> PairingLink {
        guard url.scheme?.lowercased() == scheme,
              url.host(percentEncoded: true)?.lowercased() == host,
              url.user == nil, url.password == nil, url.port == nil, url.fragment == nil,
              url.path(percentEncoded: true).isEmpty || url.path(percentEncoded: true) == "/",
              let rawQuery = url.query(percentEncoded: true),
              let params = parseQuery(rawQuery)
        else { throw .invalid }
        guard let v = params["v"], isDecimal(v), let number = Int(v) else { throw .invalid }
        if number < version { throw .outdatedPak }
        if number > version { throw .newerVersion }
        guard let id = params["id"], isLowercaseUUID(id),
              let s = params["s"], let secret = Base64URL.decode(s), secret.count == Pairing.secretSize,
              let k = params["k"], let pinBytes = Base64URL.decode(k), pinBytes.count == TLSPin.size
        else { throw .invalid }
        var endpoint: Endpoint?
        switch (params["h"], params["p"]) {
        case (nil, nil):
            endpoint = nil
        case (let h?, let p?):
            guard isIPv4(h), isDecimal(p), let port = UInt16(p), port > 0 else { throw .invalid }
            endpoint = Endpoint(host: h, port: port)
        default:
            throw .invalid
        }
        let name = params["n"].map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }.flatMap { $0.isEmpty ? nil : $0 }
        return PairingLink(deviceId: id, deviceName: name, secret: secret, endpoint: endpoint, pin: Base64URL.encode(pinBytes))
    }

    public static func parse(_ string: String) throws(PairingLinkError) -> PairingLink {
        guard let url = URL(string: string.trimmingCharacters(in: .whitespacesAndNewlines), encodingInvalidCharacters: false)
        else { throw .invalid }
        return try parse(url)
    }

    /// The link, nil if refused (see ``parse(_:)-(URL)`` for the reason).
    public init?(url: URL) {
        guard let link = try? Self.parse(url) else { return nil }
        self = link
    }

    public init?(string: String) {
        guard let link = try? Self.parse(string) else { return nil }
        self = link
    }

    /// The link, as the pak encodes it (for tests and the demo mode).
    public var url: URL {
        var items: [(String, String)] = [("v", String(Self.version)), ("id", deviceId)]
        if let deviceName { items.append(("n", deviceName)) }
        items.append(("s", Base64URL.encode(secret)))
        if let endpoint {
            items.append(("h", endpoint.host))
            items.append(("p", String(endpoint.port)))
        }
        items.append(("k", pin))
        let query = items.map { "\($0.0)=\(Signer.encodeRFC3986($0.1))" }.joined(separator: "&")
        return URL(string: "\(Self.scheme)://\(Self.host)?" + query)!
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

    private static func isDecimal(_ s: String) -> Bool {
        !s.isEmpty && s.count <= 5 && s.utf8.allSatisfy { $0 >= UInt8(ascii: "0") && $0 <= UInt8(ascii: "9") }
    }

    /// Dotted-quad IPv4, each part 0-255 without a leading zero.
    public static func isIPv4(_ s: String) -> Bool {
        let parts = s.split(separator: ".", omittingEmptySubsequences: false)
        guard parts.count == 4 else { return false }
        return parts.allSatisfy { part in
            guard !part.isEmpty, part.count <= 3, part.utf8.allSatisfy({ $0 >= UInt8(ascii: "0") && $0 <= UInt8(ascii: "9") }),
                  !(part.count > 1 && part.first == "0"), let n = Int(part)
            else { return false }
            return n <= 255
        }
    }

    static func isLowercaseUUID(_ s: String) -> Bool {
        s.range(of: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$", options: .regularExpression) != nil
    }
}
