import Foundation
import LazyXcodeCore
import SwiftTUI
import SwiftTUICLI
import Testing

@testable import LazyXcode

actor DeviceManagerRunner: CommandRunning {
    var createdName: String?
    var creationCalls = 0
    var destinationCalls = 0
    var failRefresh = false
    var compatible = true
    var destinationError = false
    let failCreation: Bool
    let noRuntimes: Bool
    let failPhysical: Bool
    let id = "A2EAD109-FD4B-4BE7-A4A1-3AD76099C121"
    init(failCreation: Bool = false, noRuntimes: Bool = false, failPhysical: Bool = false) {
        self.failCreation = failCreation
        self.noRuntimes = noRuntimes
        self.failPhysical = failPhysical
    }
    func stopRefreshing() { failRefresh = true }
    func prepareIncompatibleDevice(error: Bool) {
        createdName = "QA phone"
        compatible = false
        destinationError = error
    }
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        if arguments.starts(with: ["simctl", "create"]) {
            creationCalls += 1
            if failCreation { throw AppError("Runtime is no longer available") }
            createdName = arguments[2]
            return Data((id + "\n").utf8)
        }
        if arguments.starts(with: ["devicectl"]) {
            if failPhysical { throw AppError("Device service unavailable") }
            let index = try #require(arguments.firstIndex(of: "--json-output"))
            try Data(#"{"result":{"devices":[]}}"#.utf8).write(to: URL(fileURLWithPath: arguments[index + 1]))
            return Data()
        }
        if executable == "xcodebuild" {
            destinationCalls += 1
            if destinationError { throw AppError("Destination discovery failed") }
            if !compatible { return Data("Available destinations for the scheme:".utf8) }
            return Data(
                "Available destinations for the scheme:\n{ platform:iOS Simulator, id:\(id), OS:27.0, name:QA phone }"
                    .utf8)
        }
        if failRefresh { throw AppError("Simulator service unavailable") }
        let runtimes: [[String: Any]] =
            noRuntimes
            ? []
            : [
                [
                    "identifier": "ios", "name": "iOS 27.0", "version": "27.0", "isAvailable": true,
                    "supportedDeviceTypes": [["identifier": "phone", "name": "iPhone"]],
                ]
            ]
        let devices: [[String: Any]] =
            createdName.map { [["udid": id, "name": $0, "isAvailable": true, "state": "Shutdown"]] } ?? []
        return try JSONSerialization.data(withJSONObject: ["runtimes": runtimes, "devices": ["ios": devices]])
    }
}

@Test(arguments: [false, true]) @MainActor
func managerDoesNotSelectStaleOrIncompatibleBuildTarget(discoveryFails: Bool) async {
    let runner = DeviceManagerRunner()
    await runner.prepareIncompatibleDevice(error: discoveryFails)
    let model = deviceModel(runner: runner)
    model.focusPane(3)
    await model.deviceRefresh?.value
    model.destinations = model.managedDevices
    model.destinationID = "current"
    model.activate()
    model.activateMenu()
    await model.pending?.value
    #expect(model.destinationID == "current")
    #expect(model.pane == 3)
    #expect(model.deviceStatus.contains(discoveryFails ? "Destination discovery failed" : "not available for scheme"))
    await model.shutdown()
}

@MainActor func deviceModel(runner: DeviceManagerRunner = DeviceManagerRunner()) -> WorkspaceModel {
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "Example", path: "/DeviceManagerTest")],
        client: XcodeClient(runner: runner))
    model.scheme = "Example"
    return model
}

@Test @MainActor func keyboardCreatesSimulatorOnceAndRefreshesBuildTargets() async throws {
    let runner = DeviceManagerRunner()
    let model = deviceModel(runner: runner)
    let view = WorkspaceView(model: model, live: false)
    _ = view.handle(KeyPress(.character("4")))
    await model.deviceRefresh?.value
    #expect(model.pane == 3)
    _ = view.handle(KeyPress(.character("n")))
    #expect(model.menu?.title == "New simulator · Runtime")
    _ = view.handle(KeyPress(.return))
    #expect(model.menu?.title == "New simulator · Device model")
    _ = view.handle(KeyPress(.return))
    #expect(model.simulatorDraft?.name == "iPhone")
    _ = view.handle(KeyPress(.character("u"), modifiers: .ctrl))
    _ = view.handle(KeyPress(.return))
    #expect(model.simulatorDraft?.error == "Enter a name")
    for character in "QA phone" { _ = view.handle(KeyPress(.character(character))) }
    _ = view.handle(KeyPress(.return))
    _ = view.handle(KeyPress(.return))
    #expect(!model.requestQuit())
    await model.simulatorCreation?.value
    await model.deviceRefresh?.value
    await model.destinationRefresh?.value
    #expect(await runner.creationCalls == 1)
    #expect(await runner.createdName == "QA phone")
    #expect(model.simulatorDraft == nil)
    #expect(model.selectedDevice?.name == "QA phone")
    #expect(model.destinations.contains { $0.id == model.selectedDeviceID })
    #expect(await runner.destinationCalls == 1)
    await model.shutdown()
}

@Test @MainActor func newSimulatorLoadsInventoryAndSupportsBackAndCancelWithoutCreating() async {
    let runner = DeviceManagerRunner()
    let model = deviceModel(runner: runner)
    model.newSimulator()
    await model.pending?.value
    #expect(model.menu?.title == "New simulator · Runtime")
    model.activateMenu()
    model.back()
    #expect(model.menu?.title == "New simulator · Runtime")
    model.activateMenu()
    model.activateMenu()
    model.back()
    #expect(model.simulatorDraft == nil)
    #expect(model.menu == nil)
    #expect(await runner.creationCalls == 0)
    await model.shutdown()
}

@Test @MainActor func shutdownFinishesSimulatorCreationWithoutStartingMoreDiscovery() async {
    let runner = DeviceManagerRunner()
    let model = deviceModel(runner: runner)
    model.focusPane(3)
    await model.deviceRefresh?.value
    model.newSimulator()
    model.activateMenu()
    model.activateMenu()
    model.submitSimulator()
    await model.shutdown()
    #expect(await runner.creationCalls == 1)
    #expect(model.simulatorCreation == nil)
    #expect(model.deviceRefresh == nil)
    #expect(model.destinationRefresh == nil)
}

@Test @MainActor func simulatorErrorsKeepDraftAndLastSuccessfulInventory() async {
    let runner = DeviceManagerRunner(failCreation: true, failPhysical: true)
    let model = deviceModel(runner: runner)
    model.focusPane(3)
    await model.deviceRefresh?.value
    #expect(model.simulatorInventory != nil)
    #expect(model.deviceStatus.contains("Device service unavailable"))
    model.newSimulator()
    model.activateMenu()
    model.activateMenu()
    model.submitSimulator()
    await model.simulatorCreation?.value
    #expect(model.simulatorDraft?.error == "Runtime is no longer available")
    model.back()
    await runner.stopRefreshing()
    model.refreshDevices(force: true)
    await model.deviceRefresh?.value
    #expect(model.simulatorInventory?.runtimes.count == 1)
    #expect(model.deviceStatus.contains("Simulator service unavailable"))
    await model.shutdown()
}

@Test @MainActor func missingRuntimeExplainsHowToAddOneAndCloudCannotCreate() async {
    let runner = DeviceManagerRunner(noRuntimes: true)
    let model = deviceModel(runner: runner)
    model.focusPane(3)
    await model.deviceRefresh?.value
    model.newSimulator()
    #expect(model.menu == nil)
    #expect(model.deviceStatus.contains("Install one in Xcode"))
    model.cloudMode = true
    model.newSimulator()
    #expect(model.simulatorDraft == nil)
    #expect(await runner.creationCalls == 0)
    await model.shutdown()
}

@Test(arguments: [(120, 30), (44, 10)]) @MainActor
func devicePaneAndNameFormRenderAcrossRetainedFrames(size: (Int, Int)) async {
    let model = deviceModel()
    let renderer = DefaultRenderer()
    let view = WorkspaceView(model: model, live: false).frame(width: size.0, height: size.1)
    func frame() -> String {
        renderer.render(view, proposal: .init(width: size.0, height: size.1)).rasterSurface.lines.joined(
            separator: "\n")
    }
    model.focusPane(3)
    #expect(frame().contains("[4] Devices"))
    await model.deviceRefresh?.value
    #expect(frame().contains("No simulators"))
    model.newSimulator()
    #expect(frame().contains("Runtime"))
    model.activateMenu()
    #expect(frame().contains("Device model"))
    model.activateMenu()
    #expect(frame().contains("Name: iPhone"))
    model.simulatorDraft?.name = "QA phone"
    #expect(frame().contains("Name: QA phone"))
    model.submitSimulator()
    await model.simulatorCreation?.value
    await model.deviceRefresh?.value
    #expect(frame().contains("› QA phone"))
    #expect(!frame().contains("Name:"))
    await model.shutdown()
}
