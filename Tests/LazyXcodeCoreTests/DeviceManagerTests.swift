import Foundation
import Testing

@testable import LazyXcodeCore

private let simulatorList = Data(
    #"""
    {
      "runtimes": [
        {"identifier":"ios","name":"iOS 27.0","version":"27.0","isAvailable":true,
         "supportedDeviceTypes":[{"identifier":"phone","name":"iPhone"}]},
        {"identifier":"watch","name":"watchOS 27.0","version":"27.0","isAvailable":true,
         "supportedDeviceTypes":[{"identifier":"watch-model","name":"Apple Watch"}]},
        {"identifier":"vision","name":"visionOS 27.0","version":"27.0","isAvailable":true,"supportedDeviceTypes":[]},
        {"identifier":"ios-26","name":"iOS 26.4","version":"26.4","isAvailable":true,"supportedDeviceTypes":[]},
        {"identifier":"tv","name":"tvOS 27.0","version":"27.0","isAvailable":true,"supportedDeviceTypes":[]},
        {"identifier":"old","name":"iOS 18.0","isAvailable":false}
      ],
      "devices": {
        "ios":[{"udid":"sim","name":"QA phone","state":"Shutdown","isAvailable":true,"deviceTypeIdentifier":"phone"}],
        "old":[{"udid":"unavailable","name":"Old phone","isAvailable":false}],
        "com.apple.CoreSimulator.SimRuntime.watchOS-10-2":[{"udid":"orphan","name":"Old watch","isAvailable":false}]
      }
    }
    """#.utf8)

@Test func simulatorInventoryKeepsUnavailableDevicesButOnlyOffersInstalledCompatibleRuntimes() throws {
    let inventory = try SimulatorInventory.parse(simulatorList)
    #expect(inventory.runtimes.map(\.id) == ["ios", "ios-26", "tv", "vision", "watch"])
    #expect(inventory.runtimes.first { $0.id == "ios" }?.deviceTypes.map(\.id) == ["phone"])
    #expect(inventory.runtimes.first { $0.id == "watch" }?.deviceTypes.map(\.id) == ["watch-model"])
    #expect(inventory.devices.first { $0.id == "unavailable" }?.state == "Unavailable")
    #expect(inventory.devices.first { $0.id == "unavailable" }?.os == "18.0")
    let orphan = inventory.devices.first { $0.id == "orphan" }
    #expect(orphan?.platform == "watchOS Simulator")
    #expect(orphan?.os == "10.2")
    #expect(inventory.devices.first { $0.id == "sim" }?.os == "27.0")
    #expect(inventory.devices.first { $0.id == "sim" }?.isSimulator == true)
    #expect(throws: AppError.self) { try SimulatorInventory.parse(Data("{}".utf8)) }
}

@Test func deviceInventoryReadsOldAndNewXcodeSchemasAndExcludesSimulators() throws {
    let data = Data(
        #"""
        {"result":{"devices":[
          {"identifier":"core-id","hardwareProperties":{"udid":"usb-id","reality":"physical","platform":"iOS"},
           "deviceProperties":{"name":"Old schema phone","osVersionNumber":"18.4"},"connectionProperties":{"tunnelState":"connected"}},
          {"identifier":"new-id","properties":{"hardware":{"udid":"new-usb-id","reality":"physical","platform":"iOS"},
           "state":{"name":"New schema phone"},"software":{"osVersionNumber":{"stringValue":"27.0"}},"connection":{"state":"disconnected"}}},
          {"identifier":"sim-id","properties":{"hardware":{"reality":"simulated"}}},
          {"identifier":"old-sim-id","hardwareProperties":{"reality":"simulated"}}
        ]}}
        """#.utf8)
    let devices = try XcodeClient.parsePhysicalDevices(data)
    #expect(devices.count == 2)
    #expect(devices.allSatisfy { $0.physical == true })
    #expect(devices.first { $0.id == "usb-id" }?.state == "Connected")
    #expect(devices.first { $0.id == "new-usb-id" }?.os == "27.0")
    #expect(devices.first { $0.id == "new-usb-id" }?.state == "Disconnected")
}

private actor SimulatorCreationRunner: CommandRunning {
    var calls: [[String]] = []
    let result: String
    init(result: String = "A2EAD109-FD4B-4BE7-A4A1-3AD76099C121\n") { self.result = result }
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        calls.append([executable] + arguments)
        return Data(result.utf8)
    }
}

@Test func simulatorCreationPassesLiteralNameAndExplicitCompatibleIdentifiers() async throws {
    let runner = SimulatorCreationRunner()
    let client = XcodeClient(runner: runner)
    let runtime = try #require(SimulatorInventory.parse(simulatorList).runtimes.first { $0.id == "ios" })
    let type = try #require(runtime.deviceTypes.first)
    let id = try await client.createSimulator(name: "  QA 'phone' $(literal)  ", deviceType: type, runtime: runtime)
    #expect(id == "A2EAD109-FD4B-4BE7-A4A1-3AD76099C121")
    #expect(await runner.calls == [["xcrun", "simctl", "create", "QA 'phone' $(literal)", "phone", "ios"]])
    for name in ["", "   ", "Bad\nName"] {
        await #expect(throws: AppError.self) {
            try await client.createSimulator(name: name, deviceType: type, runtime: runtime)
        }
    }
    await #expect(throws: AppError.self) {
        try await client.createSimulator(
            name: "QA", deviceType: SimulatorDeviceType(id: "watch-model", name: "Watch"), runtime: runtime)
    }
    #expect(await runner.calls.count == 1)
    await #expect(throws: AppError.self) {
        try await XcodeClient(runner: SimulatorCreationRunner(result: "bad output")).createSimulator(
            name: "QA", deviceType: type, runtime: runtime)
    }
}
