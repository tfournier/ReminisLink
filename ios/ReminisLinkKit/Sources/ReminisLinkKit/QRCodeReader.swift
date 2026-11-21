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

    /// The payloads of the QR codes in a picture file (PNG, JPEG, HEIC...),
    /// with its orientation. Nil if the data is not a picture.
    public static func payloads(inImageData data: Data) throws -> [String]? {
        guard let source = CGImageSourceCreateWithData(data as CFData, nil),
              let image = CGImageSourceCreateImageAtIndex(source, 0, nil)
        else { return nil }
        let properties = CGImageSourceCopyPropertiesAtIndex(source, 0, nil) as? [CFString: Any]
        let orientation = (properties?[kCGImagePropertyOrientation] as? UInt32)
            .flatMap(CGImagePropertyOrientation.init(rawValue:)) ?? .up
        return try payloads(in: image, orientation: orientation)
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
