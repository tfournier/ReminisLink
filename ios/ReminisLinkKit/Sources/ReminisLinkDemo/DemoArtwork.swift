import CoreGraphics
import CoreText
import CryptoKit
import Foundation
import ImageIO

/// Original placeholder pictures for the demo mode (milestone M16), drawn
/// in code: a box art per game (gradient, geometric shapes, initials) and
/// "screenshots" for the thumbnails of save states. No real art, no logo:
/// they appear in the App Store screenshots. The same title always gives
/// the same picture.
public enum DemoArtwork {
    /// A square box art for a game title, as a PNG.
    public static func cover(for title: String, size: Int = 512) -> Data {
        let palette = Palette(seed: title)
        return render(width: size, height: size) { context, width, height in
            drawGradient(context, palette.top, palette.bottom, width: width, height: height)
            // Shapes: a large disc, a rotated square and a band of dots.
            context.setFillColor(palette.accent.copy(alpha: 0.85)!)
            let disc = CGFloat(size) * (0.42 + 0.12 * palette.unit(0))
            context.fillEllipse(in: CGRect(
                x: width * (0.15 + 0.4 * palette.unit(1)) - disc / 2, y: height * 0.62 - disc / 2, width: disc, height: disc
            ))
            context.saveGState()
            context.translateBy(x: width * (0.3 + 0.4 * palette.unit(2)), y: height * 0.45)
            context.rotate(by: .pi / 4 * (0.5 + palette.unit(3)))
            context.setFillColor(CGColor(gray: 1, alpha: 0.28))
            let square = CGFloat(size) * 0.34
            context.fill(CGRect(x: -square / 2, y: -square / 2, width: square, height: square))
            context.restoreGState()
            context.setFillColor(CGColor(gray: 1, alpha: 0.55))
            let dot = CGFloat(size) * 0.035
            for i in 0..<7 {
                let x = width * 0.1 + CGFloat(i) * width * 0.13
                context.fillEllipse(in: CGRect(x: x, y: height * 0.2, width: dot, height: dot))
            }
            drawText(initials(title), in: context, size: CGFloat(size) * 0.26, x: width * 0.08, y: height * 0.06)
        }
    }

    /// A 4:3 "screenshot" of a game for the thumbnail of a save state.
    public static func screen(for title: String, slot: Int, width: Int = 640, height: Int = 480) -> Data {
        let palette = Palette(seed: title + "#\(slot)")
        return render(width: width, height: height) { context, w, h in
            // Sky, sun, hills and blocks: a made-up level.
            drawGradient(context, palette.top, palette.bottom, width: w, height: h)
            context.setFillColor(palette.accent)
            let sun = h * 0.22
            context.fillEllipse(in: CGRect(x: w * (0.6 + 0.25 * palette.unit(0)), y: h * 0.62, width: sun, height: sun))
            context.setFillColor(palette.ground)
            context.move(to: CGPoint(x: 0, y: 0))
            var x: CGFloat = 0
            while x <= w {
                context.addLine(to: CGPoint(x: x, y: h * (0.22 + 0.08 * sin(x / w * .pi * (2 + 2 * palette.unit(1))))))
                x += 8
            }
            context.addLine(to: CGPoint(x: w, y: 0))
            context.closePath()
            context.fillPath()
            context.setFillColor(CGColor(gray: 1, alpha: 0.7))
            let block = h * 0.07
            for i in 0..<5 {
                let bx = w * (0.12 + 0.14 * CGFloat(i))
                let by = h * (0.42 + 0.05 * CGFloat(i % 2))
                context.fill(CGRect(x: bx, y: by, width: block, height: block))
            }
        }
    }

    /// The ETag of a drawn picture.
    static func etag(of data: Data) -> String {
        "\"demo-" + SHA256.hash(data: data).prefix(8).map { String(format: "%02x", $0) }.joined() + "\""
    }

    static func initials(_ title: String) -> String {
        let words = title.split { !$0.isLetter }.filter { $0.count > 2 || $0.first?.isUppercase == true }
        return String(words.prefix(2).compactMap(\.first)).uppercased()
    }

    // MARK: - Drawing

    private struct Palette {
        let bytes: [UInt8]
        let top: CGColor
        let bottom: CGColor
        let accent: CGColor
        let ground: CGColor

        init(seed: String) {
            bytes = Array(SHA256.hash(data: Data(seed.utf8)))
            let hue = Double(bytes[0]) / 255
            top = Self.color(hue: hue, saturation: 0.55, brightness: 0.95)
            bottom = Self.color(hue: (hue + 0.12).truncatingRemainder(dividingBy: 1), saturation: 0.75, brightness: 0.55)
            accent = Self.color(hue: (hue + 0.5).truncatingRemainder(dividingBy: 1), saturation: 0.6, brightness: 1)
            ground = Self.color(hue: (hue + 0.3).truncatingRemainder(dividingBy: 1), saturation: 0.6, brightness: 0.45)
        }

        /// A number in 0...1 from the seed.
        func unit(_ index: Int) -> CGFloat { CGFloat(bytes[4 + index]) / 255 }

        static func color(hue: Double, saturation: Double, brightness: Double) -> CGColor {
            let h = hue * 6
            let c = brightness * saturation
            let x = c * (1 - abs(h.truncatingRemainder(dividingBy: 2) - 1))
            let (r, g, b): (Double, Double, Double) = switch Int(h) % 6 {
            case 0: (c, x, 0)
            case 1: (x, c, 0)
            case 2: (0, c, x)
            case 3: (0, x, c)
            case 4: (x, 0, c)
            default: (c, 0, x)
            }
            let m = brightness - c
            return CGColor(srgbRed: r + m, green: g + m, blue: b + m, alpha: 1)
        }
    }

    private static func render(width: Int, height: Int, draw: (CGContext, CGFloat, CGFloat) -> Void) -> Data {
        guard let space = CGColorSpace(name: CGColorSpace.sRGB),
              let context = CGContext(
                  data: nil, width: width, height: height, bitsPerComponent: 8, bytesPerRow: 0, space: space,
                  bitmapInfo: CGImageAlphaInfo.noneSkipLast.rawValue
              )
        else { return Data() }
        draw(context, CGFloat(width), CGFloat(height))
        guard let image = context.makeImage() else { return Data() }
        let data = NSMutableData()
        guard let destination = CGImageDestinationCreateWithData(data, "public.png" as CFString, 1, nil) else { return Data() }
        CGImageDestinationAddImage(destination, image, nil)
        guard CGImageDestinationFinalize(destination) else { return Data() }
        return data as Data
    }

    private static func drawGradient(_ context: CGContext, _ top: CGColor, _ bottom: CGColor, width: CGFloat, height: CGFloat) {
        guard let gradient = CGGradient(colorsSpace: CGColorSpace(name: CGColorSpace.sRGB), colors: [bottom, top] as CFArray, locations: [0, 1])
        else { return }
        context.drawLinearGradient(gradient, start: CGPoint(x: 0, y: 0), end: CGPoint(x: width, y: height), options: [])
    }

    private static func drawText(_ text: String, in context: CGContext, size: CGFloat, x: CGFloat, y: CGFloat) {
        let font = CTFontCreateWithName("Helvetica-Bold" as CFString, size, nil)
        let attributes: [NSAttributedString.Key: Any] = [
            NSAttributedString.Key(kCTFontAttributeName as String): font,
            NSAttributedString.Key(kCTForegroundColorAttributeName as String): CGColor(gray: 1, alpha: 0.95),
        ]
        let line = CTLineCreateWithAttributedString(NSAttributedString(string: text, attributes: attributes))
        context.textPosition = CGPoint(x: x, y: y)
        CTLineDraw(line, context)
    }
}
