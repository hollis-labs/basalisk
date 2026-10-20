// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "BasaliskCompanion",
    platforms: [.macOS(.v14)],
    products: [.executable(name: "BasaliskCompanion", targets: ["BasaliskCompanion"])],
    targets: [.executableTarget(name: "BasaliskCompanion")],
    swiftLanguageModes: [.v5]
)
