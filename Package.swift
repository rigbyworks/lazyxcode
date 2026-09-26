// swift-tools-version: 6.4
import PackageDescription

let package = Package(
    name: "lazyxcode",
    platforms: [.macOS(.v15)],
    products: [.executable(name: "lazyxcode", targets: ["LazyXcode"])],
    dependencies: [
        .package(url: "https://github.com/SwiftTUI/swift-tui", exact: "0.13.5"),
        .package(url: "https://github.com/weichsel/ZIPFoundation", from: "0.9.20"),
    ],
    targets: [
        .target(name: "LazyXcodeCore", dependencies: ["ZIPFoundation"]),
        .executableTarget(
            name: "LazyXcode",
            dependencies: [
                "LazyXcodeCore", .product(name: "SwiftTUI", package: "swift-tui"),
                .product(name: "SwiftTUICLI", package: "swift-tui"),
            ]),
        .testTarget(name: "LazyXcodeCoreTests", dependencies: ["LazyXcodeCore", "ZIPFoundation"]),
        .testTarget(name: "LazyXcodeTests", dependencies: ["LazyXcode"]),
    ]
)
