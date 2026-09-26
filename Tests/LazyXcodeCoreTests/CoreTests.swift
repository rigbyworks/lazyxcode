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
