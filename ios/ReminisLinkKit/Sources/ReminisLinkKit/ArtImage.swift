import CoreGraphics
import Foundation
import ImageIO
import UniformTypeIdentifiers

/// Turns a picture chosen on the iPhone (Photos or Files: JPEG, HEIC,
/// PNG...) into the art `PUT /games/{gameId}/art` takes: a PNG whose
/// longest side is at most 1024 px (never enlarged), at most 4 MiB, upright
/// (the EXIF orientation applied) and without metadata (no location).
public enum ArtImage {
    /// Longest side of the art, in pixels.
    public static let maxSide = 1024
    /// Largest PNG the pak accepts.
    public static let maxBytes = 4 << 20

    public enum Failure: Error, Equatable, Sendable {
        /// Not a picture ImageIO can read.
        case unreadable
        /// Still larger than ``ArtImage/maxBytes`` once scaled down a lot.
        case tooLarge
    }

    /// The art made from the bytes of a picture.
    public static func png(from data: Data, maxSide: Int = ArtImage.maxSide, maxBytes: Int = ArtImage.maxBytes) throws -> Data {
        guard let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary) else {
            throw Failure.unreadable
        }
        return try png(from: source, maxSide: maxSide, maxBytes: maxBytes)
    }

    /// The art made from a picture file (read by ImageIO, not loaded whole).
    public static func png(from url: URL, maxSide: Int = ArtImage.maxSide, maxBytes: Int = ArtImage.maxBytes) throws -> Data {
        guard let source = CGImageSourceCreateWithURL(url as CFURL, [kCGImageSourceShouldCache: false] as CFDictionary) else {
            throw Failure.unreadable
        }
        return try png(from: source, maxSide: maxSide, maxBytes: maxBytes)
    }

    /// Width and height of a PNG, read from its header; nil if it is not one.
    public static func pngSize(_ data: Data) -> (width: Int, height: Int)? {
        let signature: [UInt8] = [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]
        guard data.count >= 24, Array(data.prefix(8)) == signature,
              data[data.startIndex + 12..<data.startIndex + 16].elementsEqual("IHDR".utf8)
        else { return nil }
        func be32(_ offset: Int) -> Int {
            data[data.startIndex + offset..<data.startIndex + offset + 4].reduce(0) { $0 << 8 | Int($1) }
        }
        return (be32(16), be32(20))
    }

    private static func png(from source: CGImageSource, maxSide: Int, maxBytes: Int) throws -> Data {
        guard CGImageSourceGetCount(source) > 0,
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let width = (properties[kCGImagePropertyPixelWidth] as? NSNumber)?.intValue,
              let height = (properties[kCGImagePropertyPixelHeight] as? NSNumber)?.intValue,
              width > 0, height > 0
        else { throw Failure.unreadable }
        // Never enlarged; scaled down again while the PNG is too large.
        var side = min(maxSide, max(width, height))
        while side >= 16 {
            let options: [CFString: Any] = [
                kCGImageSourceCreateThumbnailFromImageAlways: true,
                kCGImageSourceCreateThumbnailWithTransform: true,
                kCGImageSourceThumbnailMaxPixelSize: side,
                kCGImageSourceShouldCacheImmediately: true,
            ]
            guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else {
                throw Failure.unreadable
            }
            let data = try encodePNG(image)
            if data.count <= maxBytes { return data }
            side = side * 3 / 4
        }
        throw Failure.tooLarge
    }

    private static func encodePNG(_ image: CGImage) throws -> Data {
        let output = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(output, UTType.png.identifier as CFString, 1, nil) else {
            throw Failure.unreadable
        }
        CGImageDestinationAddImage(destination, image, nil)
        guard CGImageDestinationFinalize(destination) else { throw Failure.unreadable }
        return output as Data
    }
}
