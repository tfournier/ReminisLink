import CoreGraphics
import CoreVideo
import Foundation
import ImageIO
import Vision

/// Reads QR codes with Vision (M15): the camera fallback when VisionKit's
/// scanner is not supported (an iPad app on a Mac), and pictures of the
/// console's screen imported from Photos or Files. What it finds goes
/// through ``PairingLink/parse(_:)`` like a scanned code: no other path
/// to a pairing.
public enum QRCodeReader {
    /// The payloads of the QR codes in a picture.
    public static func payloads(in image: CGImage, orientation: CGImagePropertyOrientation = .up) throws -> [String] {
        try payloads(VNImageRequestHandler(cgImage: image, orientation: orientation))
    }

    /// Largest picture file read (bytes).
    public static let maxImageBytes = 50 * 1024 * 1024
    /// Largest picture read (pixels, width × height); larger ones are refused
    /// before decoding.
    public static let maxImagePixels = 200_000_000
    /// Side of the downsampled picture given to Vision (a QR code shown on
    /// a handheld's screen stays readable).
    public static let decodedSide = 3000

    public enum ImageError: Error, Equatable, Sendable {
        case notAPicture
        case tooLarge
    }

    /// The payloads of the QR codes in a picture file (PNG, JPEG, HEIC...).
    /// Bounded: at most ``maxImageBytes`` and ``maxImagePixels``; the
    /// picture is downsampled by ImageIO (orientation applied) to
    /// ``decodedSide`` before Vision, never decoded at full size.
    public static func payloads(inImageData data: Data) throws -> [String] {
        guard data.count <= maxImageBytes else { throw ImageError.tooLarge }
        guard let source = CGImageSourceCreateWithData(data as CFData, [kCGImageSourceShouldCache: false] as CFDictionary),
              CGImageSourceGetCount(source) > 0,
              let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any],
              let width = properties[kCGImagePropertyPixelWidth] as? Int,
              let height = properties[kCGImagePropertyPixelHeight] as? Int
        else { throw ImageError.notAPicture }
        guard width > 0, height > 0 else { throw ImageError.notAPicture }
        guard width <= maxImagePixels / height else { throw ImageError.tooLarge }
        let options: [CFString: Any] = [
            kCGImageSourceCreateThumbnailFromImageAlways: true,
            kCGImageSourceCreateThumbnailWithTransform: true,
            kCGImageSourceThumbnailMaxPixelSize: decodedSide,
            kCGImageSourceShouldCacheImmediately: true,
        ]
        guard let image = CGImageSourceCreateThumbnailAtIndex(source, 0, options as CFDictionary) else {
            throw ImageError.notAPicture
        }
        return try payloads(in: image)
    }

    /// The payloads of the QR codes in a camera frame.
    public static func payloads(in pixelBuffer: CVPixelBuffer, orientation: CGImagePropertyOrientation = .up) throws -> [String] {
        try payloads(VNImageRequestHandler(cvPixelBuffer: pixelBuffer, orientation: orientation))
    }

    private static func payloads(_ handler: VNImageRequestHandler) throws -> [String] {
        let request = VNDetectBarcodesRequest()
        request.symbologies = [.qr]
        try handler.perform([request])
        return (request.results ?? []).compactMap(\.payloadStringValue)
    }
}

/// The QR codes found, sorted out the way the scanner does.
public enum PairingCodeSearch: Equatable, Sendable {
    /// A pairing link this app can use.
    case link(PairingLink)
    /// A ReminisLink link this app cannot use (older or newer pak).
    case refused(PairingLinkError)
    /// QR codes, none of them a ReminisLink pairing link.
    case notAPairingCode
    /// No QR code at all.
    case noCode

    /// The first usable link wins; else a refused ReminisLink link is said;
    /// other QR codes are ignored.
    public init(payloads: [String]) {
        var refused: PairingLinkError?
        for payload in payloads {
            do {
                self = .link(try PairingLink.parse(payload))
                return
            } catch {
                if error != .invalid, refused == nil { refused = error }
            }
        }
        if let refused {
            self = .refused(refused)
        } else {
            self = payloads.isEmpty ? .noCode : .notAPairingCode
        }
    }
}

/// The one point where a pairing link found by the camera or in an
/// imported picture is accepted (M15): once, and never after the Add a
/// Console screen closed. A late import (a photo still downloading from
/// iCloud) can neither replace the camera's result nor pair after Cancel.
@MainActor
public final class PairingCodeAcceptance {
    private var isDone = false

    public init() {}

    /// Whether this link is the one to pair with (the first, while open).
    public func accept() -> Bool {
        guard !isDone else { return false }
        isDone = true
        return true
    }

    /// The screen closed: nothing is accepted any more.
    public func close() {
        isDone = true
    }

    public var isClosed: Bool { isDone }
}

/// The live scanners' filter: reports the first usable link once, and each
/// refused ReminisLink link once (the same code stays in front of the
/// camera for many frames). Other QR codes are ignored.
public struct PairingScanFilter: Sendable {
    private var found = false
    private var refused: Set<String> = []

    public init() {}

    public mutating func evaluate(_ payloads: [String]) -> Result<PairingLink, PairingLinkError>? {
        guard !found else { return nil }
        for payload in payloads {
            do {
                let link = try PairingLink.parse(payload)
                found = true
                return .success(link)
            } catch {
                guard error != .invalid, refused.insert(payload).inserted else { continue }
                return .failure(error)
            }
        }
        return nil
    }
}
