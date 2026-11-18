import CryptoKit
import Foundation
import Testing
@testable import ReminisLinkKit

@Suite("Signer")
struct SignerTests {
    let key = SymmetricKey(data: Data(repeating: 0x42, count: 32))
    let nonce = Base64URL.encode(Data(repeating: 1, count: 16))
    let emptySHA = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

    @Test func emptyBodyHash() {
        #expect(Signer.contentSHA256(Data()) == emptySHA)
    }

    @Test func nonceIsSixteenRandomBytes() throws {
        let a = Signer.makeNonce()
        let b = Signer.makeNonce()
        #expect(try #require(Base64URL.decode(a)).count == 16)
        #expect(a != b)
    }

    @Test func encodeQueryIsCanonical() throws {
        let q = Signer.encodeQuery([(name: "q", value: "Café Rêverie"), (name: "plus", value: "1+1"), (name: "path", value: "a/b")])
        #expect(q == "path=a%2Fb&plus=1%2B1&q=Caf%C3%A9%20R%C3%AAverie")
        #expect(try Signer.canonicalQuery(rawQuery: q) == q)
    }

    @Test func canonicalQueryCases() throws {
        let cases: [(String, String)] = [
            ("", ""),
            ("&&", ""),
            ("b=2&a=1", "a=1&b=2"),
            ("a=b=c", "a=b%3Dc"),
            ("a", "a="),
            ("k=%2b+%20", "k=%2B%20%20"),
            ("a=2&a=10&a=1", "a=1&a=10&a=2"),
            ("x=!*'();:@$,[]", "x=%21%2A%27%28%29%3B%3A%40%24%2C%5B%5D"),
            ("a=1&A=1&%C3%A9=1", "%C3%A9=1&A=1&a=1"),
        ]
        for (input, expected) in cases {
            #expect(try Signer.canonicalQuery(rawQuery: input) == expected, "\(input)")
        }
    }

    @Test func malformedInputsAreRejected() {
        func canonical(method: String = "GET", path: String = "/api/v1/device", query: String = "",
                       time: String = "1795539600", nonce: String? = nil, sha: String? = nil) throws -> String {
            try Signer.canonicalRequest(method: method, path: path, rawQuery: query, time: time,
                                        nonce: nonce ?? self.nonce, contentSHA256: sha ?? emptySHA)
        }
        #expect(throws: SignerError.malformedMethod) { try canonical(method: "") }
        #expect(throws: SignerError.malformedMethod) { try canonical(method: "G3T") }
        #expect(throws: SignerError.malformedMethod) { try canonical(method: "get\u{131}") }
        #expect(throws: SignerError.malformedPath) { try canonical(path: "api/v1") }
        #expect(throws: SignerError.malformedPath) { try canonical(path: "/api\n/v1") }
        #expect(throws: SignerError.malformedPath) { try canonical(path: "/api?x=1") }
        #expect(throws: SignerError.malformedQuery) { try canonical(query: "a=1\nb") }
        #expect(throws: SignerError.malformedQuery) { try canonical(query: "a=%G0") }
        #expect(throws: SignerError.malformedTime) { try canonical(time: "-1") }
        #expect(throws: SignerError.malformedTime) { try canonical(time: "01795539600") }
        #expect(throws: SignerError.malformedTime) { try canonical(time: "99999999999999999999") }
        #expect(throws: SignerError.malformedNonce) { try canonical(nonce: Base64URL.encode(Data(count: 15))) }
        #expect(throws: SignerError.malformedContentSHA256) { try canonical(sha: emptySHA.uppercased()) }
        #expect(throws: SignerError.malformedContentSHA256) { try canonical(sha: "unsigned") }
    }

    @Test func signURLRequest() throws {
        let signer = Signer(clientId: "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", key: key)
        var request = URLRequest(url: try #require(URL(string: "http://192.168.1.42:47821/api/v1/games/R2FtZQ?keepSave=true")))
        request.httpMethod = "delete"
        let signed = try signer.sign(request, time: 1_795_539_600, nonce: nonce)
        #expect(signed.httpMethod == "DELETE")
        let canonical = try Signer.canonicalRequest(
            method: "DELETE", path: "/api/v1/games/R2FtZQ", rawQuery: "keepSave=true",
            time: "1795539600", nonce: nonce, contentSHA256: emptySHA
        )
        #expect(signed.value(forHTTPHeaderField: SignedHeaders.signature) == Signer.signature(key: key, canonical: canonical))
        #expect(signed.value(forHTTPHeaderField: SignedHeaders.client) == signer.clientId)
        #expect(signed.value(forHTTPHeaderField: SignedHeaders.time) == "1795539600")
        #expect(signed.value(forHTTPHeaderField: SignedHeaders.contentSHA256) == emptySHA)
    }

    @Test func signUploadChunkUnsigned() throws {
        let signer = Signer(clientId: "3b1c8d2a-5e6f-4a7b-8c9d-0e1f2a3b4c5d", key: key)
        var request = URLRequest(url: try #require(URL(string: "http://h/api/v1/uploads/up_1")))
        request.httpMethod = "PUT"
        request.httpBody = Data("chunk".utf8)
        let signed = try signer.sign(request, time: 1, unsignedBody: true)
        #expect(signed.value(forHTTPHeaderField: SignedHeaders.contentSHA256) == "UNSIGNED")
    }

    @Test func signatureCoversBody() throws {
        let signer = Signer(clientId: "c", key: key)
        let a = try signer.headers(method: "POST", path: "/api/v1/uploads", body: Data("{}".utf8), time: 1, nonce: nonce)
        let b = try signer.headers(method: "POST", path: "/api/v1/uploads", body: Data("{ }".utf8), time: 1, nonce: nonce)
        #expect(a.signature != b.signature)
        #expect(!Signer.isValidSignature(a.signature, key: SymmetricKey(data: Data(repeating: 1, count: 32)), canonical: "x"))
    }

    @Test func base64URLStrictness() {
        #expect(Base64URL.encode(Data([0xfb, 0xff])) == "-_8")
        #expect(Base64URL.decode("-_8") == Data([0xfb, 0xff]))
        for bad in ["+/8", "-_8=", "-_9", "a", "ab\ncd", " abc", "ab%3D"] {
            #expect(Base64URL.decode(bad) == nil, "\(bad)")
        }
        #expect(Base64URL.decode("") == Data())
    }

    @Test func bodyStreamIsRefusedUnlessUnsigned() throws {
        let signer = Signer(clientId: "c", key: key)
        var request = URLRequest(url: try #require(URL(string: "http://h/api/v1/games/R2FtZQ/save")))
        request.httpMethod = "PUT"
        request.httpBodyStream = InputStream(data: Data("save".utf8))
        #expect(throws: SignerError.bodyStreamNotSignable) { try signer.sign(request, time: 1) }

        var chunk = URLRequest(url: try #require(URL(string: "http://h/api/v1/uploads/up_1")))
        chunk.httpMethod = "PUT"
        chunk.httpBodyStream = InputStream(data: Data("chunk".utf8))
        let signed = try signer.sign(chunk, time: 1, unsignedBody: true)
        #expect(signed.value(forHTTPHeaderField: SignedHeaders.contentSHA256) == "UNSIGNED")
    }
}
