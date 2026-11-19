// Renders an SVG file to a PNG with AppKit (CoreSVG), so the icons need
// nothing that is not on a Mac with Xcode.
//
// usage: swift render-svg.swift <in.svg> <out.png> <pixels> [opaque|alpha]
//
// "opaque" (the default) writes an image without an alpha channel, as App
// Store Connect requires for the app icon.
import AppKit

let args = CommandLine.arguments
guard args.count >= 4, let pixels = Int(args[3]) else {
    FileHandle.standardError.write(Data("usage: render-svg.swift <in.svg> <out.png> <pixels> [opaque|alpha]\n".utf8))
    exit(2)
}
let opaque = args.count < 5 || args[4] == "opaque"
guard let image = NSImage(contentsOf: URL(fileURLWithPath: args[1])) else {
    FileHandle.standardError.write(Data("cannot read \(args[1])\n".utf8))
    exit(1)
}
let space = CGColorSpace(name: CGColorSpace.sRGB)!
let info = opaque ? CGImageAlphaInfo.noneSkipLast.rawValue : CGImageAlphaInfo.premultipliedLast.rawValue
let context = CGContext(
    data: nil, width: pixels, height: pixels, bitsPerComponent: 8, bytesPerRow: 0, space: space, bitmapInfo: info
)!
context.interpolationQuality = .high
NSGraphicsContext.current = NSGraphicsContext(cgContext: context, flipped: false)
image.draw(in: NSRect(x: 0, y: 0, width: pixels, height: pixels))
NSGraphicsContext.current = nil
let out = NSBitmapImageRep(cgImage: context.makeImage()!)
try! out.representation(using: .png, properties: [:])!.write(to: URL(fileURLWithPath: args[2]))
