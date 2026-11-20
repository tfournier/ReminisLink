import Foundation
import Testing
import UniformTypeIdentifiers
@testable import ReminisLinkKit

/// The share extension keeps only real files (links removed from v1).
@Suite("Share attachments")
struct ShareAttachmentsTests {
    func scratch() throws -> URL {
        let url = FileManager.default.temporaryDirectory.appending(path: "rl-share-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        return url
    }

    func file(_ name: String, _ contents: String, in directory: URL) throws -> URL {
        let url = directory.appending(path: name)
        try Data(contents.utf8).write(to: url)
        return url
    }

    /// A link offered both as `public.url` and as `public.plain-text`.
    func linkProvider(_ link: String) -> NSItemProvider {
        let provider = NSItemProvider()
        for type in [UTType.url, UTType.plainText] {
            provider.registerDataRepresentation(forTypeIdentifier: type.identifier, visibility: .all) { completion in
                completion(Data(link.utf8), nil)
                return nil
            }
        }
        return provider
    }

    /// An attachment whose only representation fails to load.
    func failingProvider() -> NSItemProvider {
        let provider = NSItemProvider()
        provider.registerDataRepresentation(forTypeIdentifier: UTType.data.identifier, visibility: .all) { completion in
            completion(nil, CocoaError(.fileReadUnknown))
            return nil
        }
        return provider
    }

    @Test func aMixedShareKeepsOnlyTheFiles() async throws {
        let source = try scratch()
        let out = try scratch()
        defer {
            try? FileManager.default.removeItem(at: source)
            try? FileManager.default.removeItem(at: out)
        }
        let rom = try #require(NSItemProvider(contentsOf: try file("Game.gba", "rom bytes", in: source)))
        let cht = try #require(NSItemProvider(contentsOf: try file("Game.cht", "cheat0_desc = \"A\"\ncheat0_code = \"1\"\n", in: source)))
        let providers = [
            linkProvider("https://example.org/hack.bps"),
            rom,
            failingProvider(),
            NSItemProvider(object: URL(string: "https://example.org/a.zip")! as NSURL),
            NSItemProvider(object: "  https://example.org/x.ips\n" as NSString),
            cht,
        ]
        let result = await ShareAttachments.copyFiles(from: providers, into: out)
        #expect(result.files.map(\.lastPathComponent).sorted() == ["Game.cht", "Game.gba"])
        #expect(try String(contentsOf: try #require(result.files.first { $0.lastPathComponent == "Game.gba" }), encoding: .utf8) == "rom bytes")
        #expect(result.links == 3)
        #expect(result.failures == 1)
        // Nothing of the links is left in the scratch folder.
        let left = try FileManager.default.subpathsOfDirectory(atPath: out.path).filter { !$0.hasSuffix("/") }
        #expect(left.filter { $0.contains(".") }.map { ($0 as NSString).lastPathComponent }.sorted() == ["Game.cht", "Game.gba"])
    }

    @Test func linkRecognition() {
        #expect(ShareAttachments.isLinkText(" https://example.org/a.bps\n"))
        #expect(ShareAttachments.isLinkText("http://192.168.1.10:8000/x"))
        #expect(!ShareAttachments.isLinkText("cheat0_desc = \"https://example.org\""))
        #expect(!ShareAttachments.isLinkText("https://example.org/a b"))
        #expect(!ShareAttachments.isLinkText("file:///etc/hosts"))
        #expect(!ShareAttachments.isLinkText(""))
        #expect(ShareAttachments.isWebLinkOnly([.url, .plainText]))
        #expect(!ShareAttachments.isWebLinkOnly([.url, .fileURL, .data]))
        #expect(!ShareAttachments.isWebLinkOnly([.url, .zip]))
        #expect(ShareAttachments.fileType([.plainText, .zip]) == .zip)
        #expect(ShareAttachments.fileType([.plainText]) == .plainText)
    }
}
