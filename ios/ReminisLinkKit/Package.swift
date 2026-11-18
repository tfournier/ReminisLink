// swift-tools-version: 6.2
import PackageDescription

let strict: [SwiftSetting] = [.treatAllWarnings(as: .error)]

let package = Package(
    name: "ReminisLinkKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "ReminisLinkKit", targets: ["ReminisLinkKit"]),
        // A fake pak served through URLProtocol: tests, previews and the
        // app's demo mode. No network access.
        .library(name: "ReminisLinkDemo", targets: ["ReminisLinkDemo"]),
    ],
    targets: [
        .target(
            name: "ReminisLinkKit",
            swiftSettings: strict
        ),
        .target(
            name: "ReminisLinkDemo",
            dependencies: ["ReminisLinkKit"],
            swiftSettings: strict
        ),
        .testTarget(
            name: "ReminisLinkKitTests",
            dependencies: ["ReminisLinkKit", "ReminisLinkDemo"],
            // Copied from protocol/test-vectors by scripts/sync-test-vectors.sh.
            resources: [.copy("Resources/TestVectors")],
            swiftSettings: strict
        ),
    ]
)
