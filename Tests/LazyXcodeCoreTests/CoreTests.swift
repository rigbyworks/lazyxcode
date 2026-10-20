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

@Test func macDestinationsDoNotRetryForUnrelatedInstalledSimulators() async throws {
    actor Runner: CommandRunning {
        var destinationCalls = 0
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            if executable == "xcrun" {
                return Data(#"{"devices":{"iOS":[{"udid":"sim-1","isAvailable":true}]}}"#.utf8)
            }
            destinationCalls += 1
            return Data("Available destinations for the scheme:\n  { platform:macOS, id:mac-1, name:My Mac }".utf8)
        }
    }
    let runner = Runner()
    let destinations = try await XcodeClient(runner: runner).destinations(
        Container(kind: .project, name: "Mac", path: "/Mac"), scheme: "Mac")
    #expect(destinations.map(\.id) == ["mac-1"])
    #expect(await runner.destinationCalls == 1)
}

@Test func metadataDiscoverySkipsRemotePackageUpdates() async throws {
    actor Runner: CommandRunning {
        var discoveryArguments: [[String]] = []
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            if executable == "xcrun" { return Data(#"{"devices":{}}"#.utf8) }
            discoveryArguments.append(arguments)
            if arguments.contains("-list") { return Data(#"{"project":{"schemes":["App"]}}"#.utf8) }
            return Data("Available destinations for the scheme:\n  { platform:macOS, id:mac-1, name:My Mac }".utf8)
        }
    }
    let runner = Runner()
    let client = XcodeClient(runner: runner)
    let container = Container(kind: .project, name: "App", path: "/App")
    #expect(try await client.schemes(container) == ["App"])
    #expect(try await client.destinations(container, scheme: "App").map(\.id) == ["mac-1"])
    let arguments = await runner.discoveryArguments
    #expect(arguments.count == 2)
    #expect(arguments.allSatisfy { $0.contains("-skipPackageUpdates") })
    #expect(arguments.allSatisfy { $0.contains("-disableAutomaticPackageResolution") })
}

/// What `CommandRunner` reports when Xcode 27.1 cannot check out a pinned revision missing from the package cache.
private let checkoutFailure = """
    xcodebuild exited 74:
    Resolve Package Graph
    Checking out 1.1.0 of package ‘dep’
    Couldn’t check out revision ‘8b6482043dd132e1e505d761c99b3ca04d40d3dd’:
        fatal: unable to read tree (8b6482043dd132e1e505d761c99b3ca04d40d3dd)
    fatalError
    2026-10-20 02:44:00.578 xcodebuild[25111:54644891] Writing error result bundle to /tmp/ResultBundle.xcresult
    xcodebuild: error: Could not resolve package dependencies:
      fatalError
      Couldn’t check out revision ‘8b6482043dd132e1e505d761c99b3ca04d40d3dd’:
    """

/// Fails every metadata query with `failure` while `failsWhen` matches its arguments.
private actor PackageRunner: CommandRunning {
    let failure: String
    let failsWhen: @Sendable ([String]) -> Bool
    var xcodebuildArguments: [[String]] = []
    init(failure: String = checkoutFailure, failsWhen: @escaping @Sendable ([String]) -> Bool) {
        self.failure = failure
        self.failsWhen = failsWhen
    }
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        if executable == "xcrun" { return Data(#"{"devices":{}}"#.utf8) }
        xcodebuildArguments.append(arguments)
        if failsWhen(arguments) { throw AppError(failure) }
        if arguments.contains("-list") { return Data(#"{"project":{"schemes":["App"]}}"#.utf8) }
        return Data("Available destinations for the scheme:\n  { platform:macOS, id:mac-1, name:My Mac }".utf8)
    }
}

@Test func discoveryResolvesPackagesWhenTheCacheLacksAPinnedRevision() async throws {
    let runner = PackageRunner { $0.contains("-skipPackageUpdates") }
    let client = XcodeClient(runner: runner)
    let container = Container(kind: .project, name: "App", path: "/App")
    #expect(try await client.schemes(container) == ["App"])
    #expect(try await client.destinations(container, scheme: "App").map(\.id) == ["mac-1"])
    let packageFlags = ["-skipPackageUpdates", "-disableAutomaticPackageResolution"]
    #expect(
        await runner.xcodebuildArguments == [
            ["-project", "/App", "-list", "-json"] + packageFlags, ["-project", "/App", "-list", "-json"],
            ["-project", "/App", "-scheme", "App", "-showdestinations"] + packageFlags,
            ["-project", "/App", "-scheme", "App", "-showdestinations"],
        ])
}

@Test func unresolvablePackagesExplainTheNextStep() async throws {
    let runner = PackageRunner { _ in true }
    let error = await #expect(throws: AppError.self) {
        try await XcodeClient(runner: runner).destinations(
            Container(kind: .project, name: "App", path: "/App"), scheme: "App")
    }
    let message = try #require(error?.message)
    #expect(message.hasPrefix("Xcode could not resolve this project's Swift packages"))
    #expect(message.contains("File > Packages > Reset Package Caches, then press R."))
    #expect(
        message.hasSuffix(
            """
            Couldn’t check out revision ‘8b6482043dd132e1e505d761c99b3ca04d40d3dd’:
            fatal: unable to read tree (8b6482043dd132e1e505d761c99b3ca04d40d3dd)
            """))
    #expect(!message.contains("fatalError") && !message.contains("result bundle"))
    #expect(await runner.xcodebuildArguments.count == 2)
}

@Test func otherXcodeFailuresDoNotRetry() async throws {
    let failure = "xcodebuild exited 65:\nxcodebuild: error: The project does not contain a scheme named \"App\"."
    let runner = PackageRunner(failure: failure) { _ in true }
    let error = await #expect(throws: AppError.self) {
        try await XcodeClient(runner: runner).schemes(Container(kind: .project, name: "App", path: "/App"))
    }
    #expect(error?.message == failure)
    #expect(await runner.xcodebuildArguments.count == 1)
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
