import Foundation

/// Loads the shared test vectors copied from protocol/test-vectors by
/// scripts/sync-test-vectors.sh.
enum Vectors {
    static func url(_ name: String) throws -> URL {
        guard let url = Bundle.module.url(forResource: name, withExtension: "json", subdirectory: "TestVectors") else {
            throw CocoaError(.fileNoSuchFile, userInfo: [NSFilePathErrorKey: "TestVectors/\(name).json"])
        }
        return url
    }

    static func load<T: Decodable>(_ name: String, as type: T.Type) throws -> T {
        try JSONDecoder().decode(T.self, from: Data(contentsOf: url(name)))
    }

    /// protocol/test-vectors in the repository, found from this source file.
    static var repositoryDirectory: URL {
        URL(fileURLWithPath: #filePath)
            .deletingLastPathComponent() // ReminisLinkKitTests
            .deletingLastPathComponent() // Tests
            .deletingLastPathComponent() // ReminisLinkKit
            .deletingLastPathComponent() // ios
            .deletingLastPathComponent() // repository root
            .appendingPathComponent("protocol/test-vectors")
    }
}

struct PairingVectors: Decodable {
    struct Case: Decodable {
        let name: String
        let secret: String
        let secretHex: String
        let deviceId: String
        let clientId: String
        let clientSeed: String
        let clientPublicKey: String
        let clientNonce: String
        let deviceNonce: String
        let clientProofMessage: String
        let clientProof: String
        let deviceProofMessage: String
        let deviceProof: String
    }

    struct Link: Decodable {
        let name: String
        let deviceId: String
        let deviceName: String
        let secret: String
        let host: String
        let port: UInt16
        let pin: String
        let link: String
    }

    let cases: [Case]
    let links: [Link]
}

struct SigningVectors: Decodable {
    struct Case: Decodable {
        let name: String
        let seed: String
        let publicKey: String
        let clientId: String
        let method: String
        let path: String
        let query: String
        let time: String
        let nonce: String
        let body: String
        let contentSha256: String
        let canonicalQuery: String
        let canonicalRequest: String
        let signature: String
        let headers: [String: String]
    }

    struct InvalidQuery: Decodable {
        let name: String
        let query: String
    }

    let cases: [Case]
    let invalidQueries: [InvalidQuery]
}

struct TLSVectors: Decodable {
    let privateKeyHex: String
    let publicKeyX963Hex: String
    let spkiDerHex: String
    let certificatePem: String
    let pin: String
    let pinStdBase64: String
}

struct IDVectors: Decodable {
    struct Valid: Decodable {
        let name: String
        let path: String
        let pathUtf8Hex: String
        let id: String
    }

    struct Invalid: Decodable {
        let name: String
        let id: String
        let reason: String
    }

    let valid: [Valid]
    let invalid: [Invalid]
}

extension Data {
    var hex: String { map { String(format: "%02x", $0) }.joined() }

    init?(hex: String) {
        var bytes: [UInt8] = []
        var index = hex.startIndex
        while index < hex.endIndex {
            let next = hex.index(index, offsetBy: 2, limitedBy: hex.endIndex) ?? hex.endIndex
            guard let byte = UInt8(hex[index..<next], radix: 16) else { return nil }
            bytes.append(byte)
            index = next
        }
        self.init(bytes)
    }
}
