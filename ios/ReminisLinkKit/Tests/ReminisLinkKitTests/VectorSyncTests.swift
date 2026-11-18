import Foundation
import Testing

/// The bundled vectors must be byte-identical to protocol/test-vectors.
/// Fix a failure with scripts/sync-test-vectors.sh, never by hand.
@Suite("Test vector copies")
struct VectorSyncTests {
    @Test(arguments: ["pairing", "signing", "ids"])
    func copyMatchesRepository(name: String) throws {
        let bundled = try Data(contentsOf: Vectors.url(name))
        let source = try Data(contentsOf: Vectors.repositoryDirectory.appendingPathComponent("\(name).json"))
        #expect(bundled == source, "TestVectors/\(name).json is out of date: run scripts/sync-test-vectors.sh")
    }

    @Test func noMissingCopy() throws {
        let sources = try FileManager.default
            .contentsOfDirectory(atPath: Vectors.repositoryDirectory.path)
            .filter { $0.hasSuffix(".json") }
        #expect(!sources.isEmpty)
        for file in sources {
            #expect((try? Vectors.url(String(file.dropLast(5)))) != nil, "\(file) is not copied to the Swift tests")
        }
    }
}
