import CryptoKit
import Foundation

/// Errors raised while building a canonical request.
public enum SignerError: Error, Equatable, Sendable {
    case malformedMethod
    case malformedPath
    case malformedQuery
    case malformedTime
    case malformedNonce
    case malformedContentSHA256
    case missingURL
    /// The body is a stream: its hash cannot be signed. Use `httpBody`, or
    /// `unsignedBody` for upload chunks.
    case bodyStreamNotSignable
}

/// The five signature headers of a request.
public struct SignedHeaders: Equatable, Sendable {
    public static let client = "X-RL-Client"
    public static let time = "X-RL-Time"
    public static let nonce = "X-RL-Nonce"
    public static let contentSHA256 = "X-RL-Content-SHA256"
    public static let signature = "X-RL-Signature"

    public let clientId: String
    public let time: String
    public let nonce: String
    public let contentSHA256: String
    public let signature: String

    /// Header name → value.
    public var dictionary: [String: String] {
        [
            Self.client: clientId,
            Self.time: time,
            Self.nonce: nonce,
            Self.contentSHA256: contentSHA256,
            Self.signature: signature,
        ]
    }
}

/// Signs requests with the key shared at pairing (docs/specs/03-protocole.md §4).
///
/// Canonical string (lines joined with "\n", no trailing newline):
///
///     RL1-HMAC-SHA256
///     <METHOD>
///     <path as sent, percent-encoded, without the query>
///     <canonical query>
///     <X-RL-Time>
///     <X-RL-Nonce>
///     <X-RL-Content-SHA256>
public struct Signer: Sendable {
    public static let algorithm = "RL1-HMAC-SHA256"
    /// Replaces the body hash on `PUT /api/v1/uploads/{uploadId}` only.
    public static let unsignedPayload = "UNSIGNED"
    /// Length of `X-RL-Nonce` once decoded.
    public static let nonceSize = 16

    public let clientId: String
    private let key: SymmetricKey

    public init(clientId: String, key: SymmetricKey) {
        self.clientId = clientId
        self.key = key
    }

    // MARK: - Signing

    /// Computes the signature headers of a request.
    ///
    /// - Parameters:
    ///   - path: the path as it will be sent, percent-encoded, with `/api/v1`.
    ///   - rawQuery: the query as it will be sent (without `?`). Build it with
    ///     ``encodeQuery(_:)`` so the device reads exactly what was signed.
    ///   - unsignedBody: only for upload chunks.
    public func headers(
        method: String,
        path: String,
        rawQuery: String = "",
        body: Data = Data(),
        unsignedBody: Bool = false,
        time: Int64,
        nonce: String = Signer.makeNonce()
    ) throws -> SignedHeaders {
        let content = unsignedBody ? Self.unsignedPayload : Self.contentSHA256(body)
        let timeString = String(time)
        let canonical = try Self.canonicalRequest(
            method: method, path: path, rawQuery: rawQuery,
            time: timeString, nonce: nonce, contentSHA256: content
        )
        return SignedHeaders(
            clientId: clientId, time: timeString, nonce: nonce,
            contentSHA256: content, signature: Self.signature(key: key, canonical: canonical)
        )
    }

    /// Returns a copy of `request` with an uppercased method and the signature
    /// headers. The path and query are taken percent-encoded from its URL.
    public func sign(
        _ request: URLRequest,
        time: Int64,
        unsignedBody: Bool = false,
        nonce: String = Signer.makeNonce()
    ) throws -> URLRequest {
        guard let url = request.url else { throw SignerError.missingURL }
        if request.httpBody == nil, request.httpBodyStream != nil, !unsignedBody {
            throw SignerError.bodyStreamNotSignable
        }
        var signed = request
        let method = (request.httpMethod ?? "GET")
        let h = try headers(
            method: method,
            path: url.path(percentEncoded: true),
            rawQuery: url.query(percentEncoded: true) ?? "",
            body: request.httpBody ?? Data(),
            unsignedBody: unsignedBody,
            time: time,
            nonce: nonce
        )
        signed.httpMethod = method.uppercased()
        for (name, value) in h.dictionary {
            signed.setValue(value, forHTTPHeaderField: name)
        }
        return signed
    }

    // MARK: - Building blocks

    /// 16 random bytes in base64url.
    public static func makeNonce() -> String {
        SymmetricKey(size: .init(bitCount: nonceSize * 8)).withUnsafeBytes { Base64URL.encode(Data($0)) }
    }

    /// Lowercase hex SHA-256 of the body (the empty string's hash for no body).
    public static func contentSHA256(_ body: some DataProtocol) -> String {
        SHA256.hash(data: Data(body)).map { String(format: "%02x", $0) }.joined()
    }

    /// b64u(HMAC-SHA256(key, canonical)).
    public static func signature(key: SymmetricKey, canonical: String) -> String {
        Base64URL.encode(Data(HMAC<SHA256>.authenticationCode(for: Data(canonical.utf8), using: key)))
    }

    /// Checks a signature in constant time.
    public static func isValidSignature(_ signature: String, key: SymmetricKey, canonical: String) -> Bool {
        guard let mac = Base64URL.decode(signature) else { return false }
        return HMAC<SHA256>.isValidAuthenticationCode(mac, authenticating: Data(canonical.utf8), using: key)
    }

    /// Builds the canonical string. Rejects inputs that could break its
    /// line structure, exactly like the pak.
    public static func canonicalRequest(
        method: String,
        path: String,
        rawQuery: String,
        time: String,
        nonce: String,
        contentSHA256: String
    ) throws -> String {
        let methodBytes = Array(method.utf8)
        guard !methodBytes.isEmpty, methodBytes.allSatisfy(isASCIILetter) else {
            throw SignerError.malformedMethod
        }
        let pathBytes = Array(path.utf8)
        guard pathBytes.first == UInt8(ascii: "/"),
              !pathBytes.contains(where: { isControlOrSpace($0) || $0 == UInt8(ascii: "?") || $0 == UInt8(ascii: "#") })
        else { throw SignerError.malformedPath }
        let queryBytes = Array(rawQuery.utf8)
        guard !queryBytes.contains(where: { isControlOrSpace($0) || $0 == UInt8(ascii: "#") }) else {
            throw SignerError.malformedQuery
        }
        guard isValidTime(time) else { throw SignerError.malformedTime }
        guard let n = Base64URL.decode(nonce), n.count == nonceSize else { throw SignerError.malformedNonce }
        guard isValidContentSHA256(contentSHA256) else { throw SignerError.malformedContentSHA256 }
        let query = try canonicalQuery(rawQuery: rawQuery)
        return [algorithm, method.uppercased(), path, query, time, nonce, contentSHA256].joined(separator: "\n")
    }

    /// Canonicalizes a raw query: pairs split on "&" then on the first "=",
    /// decoded ("+" is a space, %XX a byte), re-encoded per RFC 3986, sorted by
    /// encoded key then encoded value (byte order), joined with "&".
    public static func canonicalQuery(rawQuery: String) throws -> String {
        var pairs: [(key: String, value: String)] = []
        for part in Array(rawQuery.utf8).split(separator: UInt8(ascii: "&"), omittingEmptySubsequences: true) {
            let bytes = Array(part)
            let key: [UInt8]
            let value: [UInt8]
            if let eq = bytes.firstIndex(of: UInt8(ascii: "=")) {
                key = Array(bytes[..<eq])
                value = Array(bytes[(eq + 1)...])
            } else {
                key = bytes
                value = []
            }
            pairs.append((encodeRFC3986(bytes: try unescape(key)), encodeRFC3986(bytes: try unescape(value))))
        }
        pairs.sort { a, b in
            a.key != b.key ? Array(a.key.utf8).lexicographicallyPrecedes(Array(b.key.utf8))
                : Array(a.value.utf8).lexicographicallyPrecedes(Array(b.value.utf8))
        }
        return pairs.map { "\($0.key)=\($0.value)" }.joined(separator: "&")
    }

    /// Encodes query items for sending: each key and value per RFC 3986, in
    /// canonical order. The result is its own canonical form.
    public static func encodeQuery(_ items: [(name: String, value: String)]) -> String {
        items
            .map { (encodeRFC3986($0.name), encodeRFC3986($0.value)) }
            .sorted { a, b in
                a.0 != b.0 ? Array(a.0.utf8).lexicographicallyPrecedes(Array(b.0.utf8))
                    : Array(a.1.utf8).lexicographicallyPrecedes(Array(b.1.utf8))
            }
            .map { "\($0.0)=\($0.1)" }
            .joined(separator: "&")
    }

    /// Percent-encodes every UTF-8 byte except A-Z a-z 0-9 - . _ ~.
    public static func encodeRFC3986(_ string: String) -> String {
        encodeRFC3986(bytes: Array(string.utf8))
    }

    private static let hexDigits = Array("0123456789ABCDEF".utf8)

    private static func encodeRFC3986(bytes: [UInt8]) -> String {
        var out: [UInt8] = []
        out.reserveCapacity(bytes.count)
        for c in bytes {
            if isUnreserved(c) {
                out.append(c)
            } else {
                out.append(UInt8(ascii: "%"))
                out.append(hexDigits[Int(c >> 4)])
                out.append(hexDigits[Int(c & 0x0f)])
            }
        }
        return String(decoding: out, as: UTF8.self)
    }

    private static func unescape(_ bytes: [UInt8]) throws -> [UInt8] {
        var out: [UInt8] = []
        var i = 0
        while i < bytes.count {
            let c = bytes[i]
            if c == UInt8(ascii: "+") {
                out.append(UInt8(ascii: " "))
            } else if c == UInt8(ascii: "%") {
                guard i + 2 < bytes.count, let hi = hexValue(bytes[i + 1]), let lo = hexValue(bytes[i + 2]) else {
                    throw SignerError.malformedQuery
                }
                out.append(hi << 4 | lo)
                i += 2
            } else {
                out.append(c)
            }
            i += 1
        }
        return out
    }

    private static func hexValue(_ c: UInt8) -> UInt8? {
        switch c {
        case UInt8(ascii: "0")...UInt8(ascii: "9"): return c - UInt8(ascii: "0")
        case UInt8(ascii: "a")...UInt8(ascii: "f"): return c - UInt8(ascii: "a") + 10
        case UInt8(ascii: "A")...UInt8(ascii: "F"): return c - UInt8(ascii: "A") + 10
        default: return nil
        }
    }

    private static func isUnreserved(_ c: UInt8) -> Bool {
        isASCIILetter(c) || (c >= UInt8(ascii: "0") && c <= UInt8(ascii: "9"))
            || c == UInt8(ascii: "-") || c == UInt8(ascii: ".") || c == UInt8(ascii: "_") || c == UInt8(ascii: "~")
    }

    private static func isASCIILetter(_ c: UInt8) -> Bool {
        (c >= UInt8(ascii: "A") && c <= UInt8(ascii: "Z")) || (c >= UInt8(ascii: "a") && c <= UInt8(ascii: "z"))
    }

    private static func isControlOrSpace(_ c: UInt8) -> Bool {
        c <= 0x20 || c == 0x7f
    }

    /// Canonical decimal Unix time: digits only, no sign, no leading zero.
    private static func isValidTime(_ s: String) -> Bool {
        let b = Array(s.utf8)
        guard !b.isEmpty, b.count <= 19, !(b.count > 1 && b[0] == UInt8(ascii: "0")),
              b.allSatisfy({ $0 >= UInt8(ascii: "0") && $0 <= UInt8(ascii: "9") })
        else { return false }
        return Int64(s) != nil
    }

    private static func isValidContentSHA256(_ s: String) -> Bool {
        if s == unsignedPayload { return true }
        let b = Array(s.utf8)
        return b.count == 64 && b.allSatisfy {
            ($0 >= UInt8(ascii: "0") && $0 <= UInt8(ascii: "9")) || ($0 >= UInt8(ascii: "a") && $0 <= UInt8(ascii: "f"))
        }
    }
}
