// swift-tools-version: 6.2
import PackageDescription

let strict: [SwiftSetting] = [.treatAllWarnings(as: .error)]

let package = Package(
    name: "ReminisLinkKit",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "ReminisLinkKit", targets: ["ReminisLinkKit"]),
    ],
    targets: [
        .target(
            name: "ReminisLinkKit",
            swiftSettings: strict
        ),
        .testTarget(
            name: "ReminisLinkKitTests",
            dependencies: ["ReminisLinkKit"],
            // Copied from protocol/test-vectors by scripts/sync-test-vectors.sh.
            resources: [.copy("Resources/TestVectors")],
            swiftSettings: strict
        ),
    ]
)
