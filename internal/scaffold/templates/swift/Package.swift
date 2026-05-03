// swift-tools-version: 6.0
import PackageDescription
let package = Package(
    name: "{{name}}",
    dependencies: [.package(url: "https://github.com/vapor/vapor.git", from: "4.106.0")],
    targets: [.executableTarget(name: "{{name}}", dependencies: [.product(name: "Vapor", package: "vapor")])]
)
