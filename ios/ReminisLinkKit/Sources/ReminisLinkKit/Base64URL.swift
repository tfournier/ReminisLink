import Foundation

/// Base64url without padding (RFC 4648 §5), as used everywhere in the protocol.
public enum Base64URL {
    /// Encodes bytes as base64url without padding.
    public static func encode(_ data: some DataProtocol) -> String {
        Data(data).base64EncodedString()
            .replacingOccurrences(of: "+", with: "-")
            .replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }

    /// Decodes canonical base64url without padding. Returns nil for any other
    /// input: padding, the standard alphabet, whitespace, an impossible length
    /// or non-zero trailing bits.
    public static func decode(_ string: String) -> Data? {
        let bytes = Array(string.utf8)
        guard bytes.allSatisfy(isAlphabet), bytes.count % 4 != 1 else { return nil }
        var standard = string
            .replacingOccurrences(of: "-", with: "+")
            .replacingOccurrences(of: "_", with: "/")
        standard += String(repeating: "=", count: (4 - bytes.count % 4) % 4)
        guard let data = Data(base64Encoded: standard), encode(data) == string else { return nil }
        return data
    }

    private static func isAlphabet(_ c: UInt8) -> Bool {
        switch c {
        case UInt8(ascii: "A")...UInt8(ascii: "Z"),
             UInt8(ascii: "a")...UInt8(ascii: "z"),
             UInt8(ascii: "0")...UInt8(ascii: "9"),
             UInt8(ascii: "-"), UInt8(ascii: "_"):
            return true
        default:
            return false
        }
    }
}
