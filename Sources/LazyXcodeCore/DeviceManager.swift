import Foundation

public struct SimulatorDeviceType: Identifiable, Sendable, Equatable {
    public let id: String
    public let name: String
}

public struct SimulatorRuntime: Identifiable, Sendable, Equatable {
    public let id: String
    public let name: String
    public let deviceTypes: [SimulatorDeviceType]
}

public struct SimulatorInventory: Sendable {
    public let devices: [Destination]
    public let runtimes: [SimulatorRuntime]

    public static func parse(_ data: Data) throws -> Self {
        let document = try JSONValue.decode(data)
        guard case .array = document["runtimes"], case .object = document["devices"] else {
            throw AppError("Xcode returned an invalid simulator list")
        }
        let runtimeValues = document["runtimes"].array
        let runtimes = runtimeValues.filter { $0["isAvailable"].bool }.map { runtime in
            SimulatorRuntime(
                id: runtime["identifier"].string, name: runtime["name"].string,
                deviceTypes: runtime["supportedDeviceTypes"].array.compactMap { type in
                    guard !type["identifier"].string.isEmpty, !type["name"].string.isEmpty else { return nil }
                    return SimulatorDeviceType(id: type["identifier"].string, name: type["name"].string)
                }.sorted { $0.name.localizedStandardCompare($1.name) == .orderedAscending })
        }.sorted { $0.name.localizedStandardCompare($1.name) == .orderedDescending }
        let devices = document["devices"].object.flatMap { runtimeID, values in
            let runtime = runtimeValues.first { $0["identifier"].string == runtimeID }
            let platform = runtime?["name"].string.split(separator: " ").first.map(String.init) ?? "Unknown"
            return values.array.compactMap { value -> Destination? in
                guard !value["udid"].string.isEmpty else { return nil }
                var device = Destination(
                    id: value["udid"].string, name: value["name"].string,
                    os: runtime?["version"].string ?? runtimeID,
                    platform: platform + " Simulator",
                    state: value["isAvailable"].bool ? value["state"].string : "Unavailable")
                device.deviceType = value["deviceTypeIdentifier"].string
                return device
            }
        }.sorted { ($0.platform, $0.name, $0.os, $0.id) < ($1.platform, $1.name, $1.os, $1.id) }
        return Self(devices: devices, runtimes: runtimes)
    }
}

extension XcodeClient {
    public func simulatorInventory() async throws -> SimulatorInventory {
        try await SimulatorInventory.parse(runner.run("xcrun", ["simctl", "list", "--json"]))
    }

    public func physicalDevices() async throws -> [Destination] {
        // A file works with both older devicectl versions and versions supporting stdout JSON.
        let file = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString + ".json")
        defer { try? FileManager.default.removeItem(at: file) }
        try await runner.run("xcrun", ["devicectl", "list", "devices", "--json-output", file.path, "--timeout", "10"])
        return try Self.parsePhysicalDevices(Data(contentsOf: file))
    }

    public static func parsePhysicalDevices(_ data: Data) throws -> [Destination] {
        let document = try JSONValue.decode(data)
        guard case .array = document["result"]["devices"] else {
            throw AppError("Xcode returned an invalid device list")
        }
        return document["result"]["devices"].array.compactMap { device in
            let properties = device["properties"]
            let hardware = properties["hardware"] == .null ? device["hardwareProperties"] : properties["hardware"]
            guard hardware["reality"].string != "simulated" else { return nil }
            let legacy = device["deviceProperties"]
            let name = properties["state"]["name"].string
            let version = properties["software"]["osVersionNumber"]["stringValue"].string
            let connection = properties["connection"]["state"].string
            let state = connection.isEmpty ? device["connectionProperties"]["tunnelState"].string : connection
            let id = hardware["udid"].string.isEmpty ? device["identifier"].string : hardware["udid"].string
            guard !id.isEmpty else { return nil }
            return Destination(
                id: id, name: name.isEmpty ? legacy["name"].string : name,
                os: version.isEmpty ? legacy["osVersionNumber"].string : version,
                platform: hardware["platform"].string,
                state: state.isEmpty ? "Unknown" : state.capitalized, physical: true)
        }.sorted { ($0.name, $0.id) < ($1.name, $1.id) }
    }

    public func createSimulator(name: String, deviceType: SimulatorDeviceType, runtime: SimulatorRuntime) async throws
        -> String
    {
        let name = name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, !name.unicodeScalars.contains(where: { CharacterSet.controlCharacters.contains($0) })
        else {
            throw AppError("Enter a simulator name without control characters")
        }
        guard runtime.deviceTypes.contains(where: { $0.id == deviceType.id }) else {
            throw AppError("This device model does not support the selected runtime")
        }
        let data = try await runner.run("xcrun", ["simctl", "create", name, deviceType.id, runtime.id])
        let id = String(decoding: data, as: UTF8.self).trimmingCharacters(in: .whitespacesAndNewlines)
        guard UUID(uuidString: id) != nil else {
            throw AppError("Xcode did not return a simulator identifier. Refresh Devices before trying again.")
        }
        return id
    }
}
