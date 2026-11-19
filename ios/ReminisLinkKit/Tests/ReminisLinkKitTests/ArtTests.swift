import CoreGraphics
import Foundation
import ImageIO
import ReminisLinkDemo
import Testing
import UniformTypeIdentifiers
@testable import ReminisLinkKit

/// A picture as Photos or Files would give it, drawn here (no file of the
/// repository): a gradient, or noise that PNG cannot compress.
func makePicture(width: Int, height: Int, type: UTType = .jpeg, noise: Bool = false, orientation: Int? = nil) throws -> Data {
    let context = try #require(CGContext(
        data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0,
        space: CGColorSpaceCreateDeviceRGB(), bitmapInfo: CGImageAlphaInfo.premultipliedLast.rawValue
    ))
    if noise, let pixels = context.data?.assumingMemoryBound(to: UInt32.self) {
        var x: UInt32 = 0x2545_F491
        for i in 0..<(context.bytesPerRow / 4 * height) {
            x ^= x << 13; x ^= x >> 17; x ^= x << 5
            pixels[i] = x | 0xFF00_0000
        }
    } else {
        for column in stride(from: 0, to: width, by: max(1, width / 32)) {
            context.setFillColor(CGColor(red: CGFloat(column) / CGFloat(width), green: 0.4, blue: 0.7, alpha: 1))
            context.fill(CGRect(x: column, y: 0, width: max(1, width / 32), height: height))
        }
    }
    let image = try #require(context.makeImage())
    let output = NSMutableData()
    let destination = try #require(CGImageDestinationCreateWithData(output, type.identifier as CFString, 1, nil))
    var properties: [CFString: Any] = [:]
    if let orientation { properties[kCGImagePropertyOrientation] = orientation }
    CGImageDestinationAddImage(destination, image, properties as CFDictionary)
    #expect(CGImageDestinationFinalize(destination))
    return output as Data
}

@Suite("Game art (M8)")
struct ArtTests {
    @Test func aLargePhotoIsScaledDownToALongestSideOf1024() throws {
        let png = try ArtImage.png(from: try makePicture(width: 3000, height: 1500))
        let size = try #require(ArtImage.pngSize(png))
        #expect(size.width == 1024)
        #expect(size.height == 512)
        #expect(png.count <= ArtImage.maxBytes)
    }

    @Test func aSmallPictureIsNeverEnlarged() throws {
        let png = try ArtImage.png(from: try makePicture(width: 120, height: 90, type: .png))
        let size = try #require(ArtImage.pngSize(png))
        #expect(size.width == 120)
        #expect(size.height == 90)
    }

    /// The EXIF orientation is applied: a portrait photo stays portrait.
    @Test func theOrientationIsApplied() throws {
        // Stored landscape, shown rotated by 90° (orientation 6).
        let png = try ArtImage.png(from: try makePicture(width: 2000, height: 1000, orientation: 6))
        let size = try #require(ArtImage.pngSize(png))
        #expect(size.width == 512)
        #expect(size.height == 1024)
    }

    /// Noise does not compress: the PNG is scaled down until it fits.
    @Test func aPNGTooLargeIsScaledDownUntilItFits() throws {
        // 1024 x 1024 of noise is about 3 MiB: checked against 1 MiB.
        let limit = 1 << 20
        let picture = try makePicture(width: 1024, height: 1024, type: .png, noise: true)
        #expect(picture.count > limit)
        let png = try ArtImage.png(from: picture, maxBytes: limit)
        #expect(png.count <= limit)
        let size = try #require(ArtImage.pngSize(png))
        #expect(size.width < 1024)
        #expect(size.width == size.height)
    }

    @Test func aFileThatIsNotAPictureIsRefused() {
        #expect(throws: ArtImage.Failure.unreadable) { try ArtImage.png(from: Data("not a picture".utf8)) }
        #expect(ArtImage.pngSize(Data("not a png".utf8)) == nil)
    }

    @Test func aPictureFileIsReadFromItsURL() throws {
        let url = FileManager.default.temporaryDirectory.appending(path: "art-\(UUID().uuidString).jpg")
        defer { try? FileManager.default.removeItem(at: url) }
        try makePicture(width: 2048, height: 2048).write(to: url)
        let size = try #require(ArtImage.pngSize(try ArtImage.png(from: url)))
        #expect(size.width == 1024 && size.height == 1024)
    }

    /// Set, then removed, against the fake pak: the body is signed with
    /// its SHA-256 (checked by the fake), the listing follows, and the
    /// invalidated cache gives the new art at once.
    @Test func artIsSetThenRemovedAndTheCacheFollows() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let cache = ArtCache(directory: FileManager.default.temporaryDirectory.appending(path: "art-\(UUID().uuidString)"))
        defer { try? FileManager.default.removeItem(at: cache.directory) }
        let system = try #require(try await client.systems().first { $0.tag == "GBA" })
        let game = try #require(try await client.games(systemId: system.id).games.first { $0.hasArt })
        #expect(try await cache.load(gameId: game.id, client: client) == FakePak.artPNG)

        let png = try ArtImage.png(from: try makePicture(width: 640, height: 480))
        let updated = try await client.setArt(gameId: game.id, png: png)
        #expect(updated.hasArt)
        #expect(pak.log.last?.status == 200)
        cache.invalidate(deviceId: pak.deviceId, gameId: game.id)
        #expect(cache.entry(deviceId: pak.deviceId, gameId: game.id) == nil)
        #expect(try await cache.load(gameId: game.id, client: client) == png)
        #expect(pak.trashedArtCount == 1)
        let detail = try await client.game(id: game.id)
        #expect(detail.files.first { $0.role == .art }?.sizeBytes == Int64(png.count))

        let removed = try await client.deleteArt(gameId: game.id)
        #expect(!removed.hasArt)
        cache.invalidate(deviceId: pak.deviceId, gameId: game.id)
        #expect(try await cache.load(gameId: game.id, client: client) == nil)
        #expect(try await client.games(systemId: system.id).games.first { $0.id == game.id }?.hasArt == false)
        #expect(pak.trashedArtCount == 2)
    }

    @Test func theFakePakRefusesWhatThePakRefuses() async throws {
        let pak = makePak()
        let client = try await pairedClient(pak)
        let system = try #require(try await client.systems().first { $0.tag == "GBA" })
        let game = try #require(try await client.games(systemId: system.id).games.first)
        // A JPEG is not a PNG; a PNG over 1024 px is refused.
        for body in [try makePicture(width: 64, height: 64), try makePicture(width: 1100, height: 10, type: .png)] {
            do {
                _ = try await client.setArt(gameId: game.id, png: body)
                Issue.record("expected 422")
            } catch let APIError.server(error) {
                #expect(error.code == .unsupportedFormat)
            }
        }
        // Over 4 MiB: refused before sending.
        await #expect(throws: APIError.self) { try await client.setArt(gameId: game.id, png: Data(count: ArtImage.maxBytes + 1)) }
    }
}
