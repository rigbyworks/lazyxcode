import Foundation

public struct XcodeClient: Sendable {
    public let runner: any CommandRunning
    public init(runner: any CommandRunning = CommandRunner()) { self.runner = runner }

    public func developerDirectory() async throws -> String {
        String(decoding: try await runner.run("xcode-select", ["-p"]), as: UTF8.self)
            .trimmingCharacters(in: .whitespacesAndNewlines)
    }
    public func openInXcode(_ path: String) async throws {
        let app = URL(fileURLWithPath: try await developerDirectory()).deletingLastPathComponent()
            .deletingLastPathComponent()
        try await runner.run("open", ["-a", app.path, path])
    }
    public func checkEnvironment() async throws {
        let directory = try await developerDirectory()
        guard directory.hasPrefix("/"), FileManager.default.fileExists(atPath: directory + "/usr/bin/xcodebuild") else {
            throw AppError("Full Xcode is required. Select it with xcode-select or DEVELOPER_DIR.")
        }
        let version = String(decoding: try await runner.run("xcodebuild", ["-version"]), as: UTF8.self)
        let numbers = version.components(separatedBy: .newlines)[0].replacingOccurrences(of: "Xcode ", with: "").split(
            separator: "."
        ).compactMap { Int($0) }
        guard let major = numbers.first, major > 16 || major == 16 && numbers.count > 1 && numbers[1] >= 3 else {
            throw AppError("Xcode 16.3 or newer is required; found \(version)")
        }
    }
    public func schemes(_ container: Container) async throws -> [String] {
        let data = try await runner.run("xcodebuild", container.arguments + ["-list", "-json"])
        let document = try JSONValue.decode(data)
        return document[container.kind.rawValue]["schemes"].array.map(\.string).sorted()
    }
    public func destinations(_ container: Container, scheme: String) async throws -> [Destination] {
        async let destinations = runner.run(
            "xcodebuild", container.arguments + ["-scheme", scheme, "-showdestinations"])
        async let devices = runner.run("xcrun", ["simctl", "list", "devices", "available", "--json"])
        return try await Self.parseDestinations(String(decoding: destinations, as: UTF8.self), devices: devices)
    }
    public static func parseDestinations(_ text: String, devices: Data) throws -> [Destination] {
        let groups = try JSONValue.decode(devices)["devices"].object.values
        var available: [String: JSONValue] = [:]
        for device in groups.flatMap(\.array) where device["isAvailable"].bool {
            available[device["udid"].string] = device
        }
        var result: [Destination] = []
        var eligible = false
        for raw in text.components(separatedBy: .newlines) {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.hasPrefix("Available destinations") || line.hasPrefix("Destinations compatible") {
                eligible = true
                continue
            }
            if line.hasPrefix("Ineligible destinations") || line.hasPrefix("Unavailable destinations")
                || line.hasPrefix("Destinations incompatible")
            {
                eligible = false
            }
            guard eligible, line.hasPrefix("{"), line.hasSuffix("}") else { continue }
            var fields: [String: String] = [:]
            for part in line.dropFirst().dropLast().split(separator: ",") {
                let pair = part.split(separator: ":", maxSplits: 1).map { $0.trimmingCharacters(in: .whitespaces) }
                if pair.count == 2 { fields[pair[0]] = pair[1] }
            }
            guard let id = fields["id"], !id.contains(":placeholder"), let platform = fields["platform"] else {
                continue
            }
            let simulator = platform.contains("Simulator")
            guard !simulator || available[id] != nil else { continue }
            guard simulator || ["macOS", "iOS", "tvOS", "watchOS", "visionOS"].contains(platform) else { continue }
            guard !result.contains(where: { $0.id == id }) else { continue }
            result.append(
                Destination(
                    id: id, name: fields["name"] ?? id, os: fields["OS"] ?? "", platform: platform,
                    state: simulator ? available[id]?["state"].string ?? "" : "Connected",
                    physical: !simulator && platform != "macOS"))
        }
        return result.sorted { ($0.platform, $0.name, $0.os) < ($1.platform, $1.name, $1.os) }
    }
    public func arguments(for record: BuildRecord) -> [String] {
        var arguments =
            record.container.arguments + [
                "-scheme", record.scheme, "-destination", "id=\(record.simulator.id)",
                "-derivedDataPath", record.derivedDataKey, "-showBuildTimingSummary",
            ]
        if record.operation == .test || record.operation == .discoverTests {
            arguments += (record.testTargets ?? []).map { "-only-testing:\($0)" }
            if record.operation == .discoverTests, let path = record.enumerationPath {
                arguments += [
                    "-enumerate-tests", "-test-enumeration-style", "flat", "-test-enumeration-format", "json",
                    "-test-enumeration-output-path", path,
                ]
            } else {
                if let path = record.resultBundlePath { arguments += ["-resultBundlePath", path] }
                arguments += ["-enableCodeCoverage", record.coverage == true ? "YES" : "NO"]
            }
            arguments += ["test"]
        } else {
            arguments += ["build"]
        }
        return arguments
    }
    public func build(_ record: BuildRecord, log: ActivityLog) async throws {
        try await runner.run("xcodebuild", arguments(for: record), output: { log.append($0) })
    }
    public func product(_ record: BuildRecord) async throws -> Product {
        let arguments =
            record.container.arguments + [
                "-scheme", record.scheme, "-destination", "id=\(record.simulator.id)",
                "-derivedDataPath", record.derivedDataKey, "-showBuildSettings", "-json",
            ]
        let data = try await runner.run("xcodebuild", arguments)
        let products = try JSONValue.decode(data).array.compactMap { value -> Product? in
            let settings = value["buildSettings"]
            guard settings["WRAPPER_EXTENSION"].string == "app", settings["SKIP_INSTALL"].string != "YES",
                !settings["PRODUCT_BUNDLE_IDENTIFIER"].string.isEmpty, !settings["FULL_PRODUCT_NAME"].string.isEmpty
            else { return nil }
            return Product(
                appPath: settings["TARGET_BUILD_DIR"].string + "/" + settings["FULL_PRODUCT_NAME"].string,
                bundleID: settings["PRODUCT_BUNDLE_IDENTIFIER"].string)
        }
        guard products.count == 1 else { throw AppError("Expected one runnable app product, found \(products.count)") }
        return products[0]
    }
    public func boot(_ destination: Destination) async throws {
        guard destination.isSimulator else { return }
        do { try await runner.run("xcrun", ["simctl", "boot", destination.id]) } catch {
            if !error.localizedDescription.contains("current state: Booted") { throw error }
        }
        let developer = try await developerDirectory()
        let hub = URL(fileURLWithPath: developer).deletingLastPathComponent().appendingPathComponent(
            "Applications/DeviceHub.app")
        if FileManager.default.fileExists(atPath: hub.path) {
            var components = URLComponents(string: "devices://device/open")!
            components.queryItems = [URLQueryItem(name: "id", value: destination.id)]
            try await runner.run("open", ["-a", hub.path, components.string!])
        } else {
            try await runner.run(
                "open",
                [
                    "-a", developer + "/Applications/Simulator.app", "--args", "-CurrentDeviceUDID", destination.id,
                    "-AttachBootedOnStart", "NO",
                ])
        }
        try await runner.run("xcrun", ["simctl", "bootstatus", destination.id, "-b"])
    }
    public func install(_ product: Product, on destination: Destination) async throws {
        guard !destination.isMac else { return }
        let arguments =
            destination.isSimulator
            ? ["simctl", "install", destination.id, product.appPath]
            : ["devicectl", "device", "install", "app", "--device", destination.id, product.appPath]
        try await runner.run("xcrun", arguments)
    }
    public func launch(_ product: Product, on destination: Destination, log: ActivityLog) async throws {
        if destination.isMac {
            try await runner.run("open", [product.appPath])
            return
        }
        let arguments =
            destination.isSimulator
            ? ["simctl", "launch", "--console-pty", "--terminate-running-process", destination.id, product.bundleID]
            : [
                "devicectl", "device", "process", "launch", "--console", "--terminate-existing", "--device",
                destination.id, product.bundleID,
            ]
        try await runner.run("xcrun", arguments, output: { log.append($0) })
    }
}
