import Foundation
import Testing

@testable import LazyXcodeCore

@Test func workspaceDiscoveryIsShallowAndSorted() throws {
    let directory = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: directory) }
    for name in ["App.xcodeproj", "App.xcworkspace", "Nested/Hidden.xcodeproj"] {
        try FileManager.default.createDirectory(
            at: directory.appendingPathComponent(name), withIntermediateDirectories: true)
    }
    let containers = try Container.discover(in: directory)
    #expect(containers.map(\.kind) == [.workspace, .project])
}

@Test func destinationFiltering() throws {
    let devices = Data(
        #"{"devices":{"iOS":[{"udid":"sim-1","name":"Phone","isAvailable":true,"state":"Booted"}]}}"#.utf8)
    let output = """
        Available destinations for the scheme:
          { platform:iOS Simulator, id:sim-1, OS:18.4, name:Phone }
          { platform:iOS Simulator, id:missing, name:Missing }
          { platform:iOS, id:device-1, name:Matt's Phone }
          { platform:macOS, arch:arm64, id:mac-1, name:My Mac }
          { platform:macOS, arch:x86_64, id:mac-1, name:My Mac }
          { platform:iOS, id:dvtdevice-DVTiPhonePlaceholder-iphoneos:placeholder, name:Any iOS Device }
        Ineligible destinations for the scheme:
          { platform:iOS, id:unavailable, name:Unavailable }
        """
    let destinations = try XcodeClient.parseDestinations(output, devices: devices)
    #expect(destinations.count == 3)
    #expect(destinations.first { $0.id == "sim-1" }?.isSimulator == true)
    #expect(destinations.first { $0.id == "device-1" }?.physical == true)
}

/// Stands in for Xcode answering `-showdestinations` without its simulators for the first `missing` calls.
private actor FlakyDestinationRunner: CommandRunning {
    let missing: Int
    var destinationCalls = 0
    init(missing: Int) { self.missing = missing }
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        if executable == "xcrun" {
            return Data(#"{"devices":{"iOS":[{"udid":"sim-1","isAvailable":true,"state":"Shutdown"}]}}"#.utf8)
        }
        destinationCalls += 1
        var text = """
            Available destinations for the scheme:
              { platform:iOS, id:device-1, name:Phone }
              { platform:iOS Simulator, id:simulator:placeholder, name:Any iOS Simulator Device }
            """
        if destinationCalls > missing { text += "\n  { platform:iOS Simulator, id:sim-1, OS:18.4, name:iPhone }" }
        return Data(text.utf8)
    }
}

@Test func destinationsRetryWhenXcodeOmitsSimulators() async throws {
    let runner = FlakyDestinationRunner(missing: 2)
    let destinations = try await XcodeClient(runner: runner).destinations(
        Container(kind: .project, name: "App", path: "/App"), scheme: "App")
    #expect(destinations.map(\.id).sorted() == ["device-1", "sim-1"])
    #expect(await runner.destinationCalls == 3)
}

@Test func destinationsFailRatherThanDropEverySimulator() async throws {
    let runner = FlakyDestinationRunner(missing: 3)
    await #expect(throws: AppError.self) {
        try await XcodeClient(runner: runner).destinations(
            Container(kind: .project, name: "App", path: "/App"), scheme: "App")
    }
    #expect(await runner.destinationCalls == 3)
}

@Test func testCaseIdentifiersKeepBundleAndIgnoreRepetitions() throws {
    let data = Data(
        #"{"testNodes":[{"nodeType":"Unit test bundle","name":"AppTests.xctest","children":[{"nodeType":"Test Case","nodeIdentifier":"Suite/test()","nodeIdentifierURL":"test://bundle/Suite/test","result":"Failed","children":[{"nodeType":"Test Case","nodeIdentifier":"argument"}]}]}]}"#
            .utf8)
    let tests = try XcodeClient.parseTestResults(data)
    #expect(tests.count == 1)
    #expect(tests[0].identifier == "AppTests/Suite/test()")
    #expect(tests[0].id == "test://bundle/Suite/test")
    #expect(tests[0].failed)
}

@Test func openInXcodeUsesSelectedXcode() async throws {
    actor Runner: CommandRunning {
        var calls: [[String]] = []
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            calls.append([executable] + arguments)
            if executable == "xcode-select" { return Data("/Applications/Xcode-beta 2.app/Contents/Developer\n".utf8) }
            return Data()
        }
    }
    let runner = Runner()
    try await XcodeClient(runner: runner).openInXcode("/App/App.xcodeproj")
    #expect(await runner.calls.last == ["open", "-a", "/Applications/Xcode-beta 2.app", "/App/App.xcodeproj"])
}
