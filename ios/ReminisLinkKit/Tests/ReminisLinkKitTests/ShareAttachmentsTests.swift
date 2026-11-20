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
        #expect(ShareAttachments.isLinkText("https://example.org/" + String(repeating: "a", count: 10_000)))
        #expect(ShareAttachments.fileType([.plainText, .zip]) == .zip)
        #expect(ShareAttachments.fileType([.plainText]) == .plainText)
        #expect(ShareAttachments.fileType([.url]) == nil)
        #expect(ShareAttachments.mayStartWithLink(Data("\u{FEFF} https://x".utf8)))
        #expect(!ShareAttachments.mayStartWithLink(Data("cheat0_desc".utf8)))
    }

    /// One attachment offering a real `.cht` as `public.plain-text` and also
    /// `public.url`: the file is kept, with its name.
    @Test func aTextFileThatAlsoOffersAURLIsKept() async throws {
        let source = try scratch()
        let out = try scratch()
        defer {
            try? FileManager.default.removeItem(at: source)
            try? FileManager.default.removeItem(at: out)
        }
        let cht = try file("export.txt", "cheats = 1\ncheat0_desc = \"See https://example.org\"\ncheat0_code = \"1\"\n", in: source)
        let provider = NSItemProvider()
        provider.registerFileRepresentation(forTypeIdentifier: UTType.plainText.identifier, fileOptions: [], visibility: .all) { completion in
            completion(cht, false, nil)
            return nil
        }
        provider.registerDataRepresentation(forTypeIdentifier: UTType.url.identifier, visibility: .all) { completion in
            completion(Data("https://example.org/Game.cht".utf8), nil)
            return nil
        }
        provider.suggestedName = "Game.cht"
        let result = await ShareAttachments.copyFiles(from: [provider], into: out)
        #expect(result.links == 0 && result.failures == 0)
        let kept = try #require(result.files.first)
        #expect(kept.lastPathComponent == "Game.cht", "\(kept.lastPathComponent)")
        #expect(try Data(contentsOf: kept) == Data(contentsOf: cht))
    }

    /// A link shared only as text is dropped in UTF-16 (with or without a
    /// byte order mark) and whatever its length.
    @Test func linksAsTextAreDroppedInAnyEncodingAndLength() async throws {
        let out = try scratch()
        defer { try? FileManager.default.removeItem(at: out) }
        func textProvider(_ data: Data) -> NSItemProvider {
            let provider = NSItemProvider()
            provider.registerDataRepresentation(forTypeIdentifier: UTType.plainText.identifier, visibility: .all) { completion in
                completion(data, nil)
                return nil
            }
            return provider
        }
        let link = "https://example.org/hack.bps"
        let long = "https://example.org/" + String(repeating: "a", count: 10_000) + ".zip\n"
        let providers = [
            textProvider(link.data(using: .utf16)!),                 // with a byte order mark
            textProvider(link.data(using: .utf16LittleEndian)!),
            textProvider(link.data(using: .utf16BigEndian)!),
            textProvider(Data(long.utf8)),
            textProvider(Data("cheat0_desc = \"A\"\ncheat0_code = \"1\"\n".utf16.flatMap { [UInt8($0 & 0xFF), UInt8($0 >> 8)] })),
        ]
        let result = await ShareAttachments.copyFiles(from: providers, into: out)
        #expect(result.links == 4)
        #expect(result.files.count == 1)
        #expect(result.failures == 0)
    }
}
