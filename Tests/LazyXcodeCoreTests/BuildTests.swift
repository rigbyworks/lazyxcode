import Foundation
import Synchronization
import Testing

@testable import LazyXcodeCore

actor BlockingRunner: CommandRunning {
    var calls: [[String]] = []
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        calls.append([executable] + arguments)
        output?(Data("CompileSwift App.swift\n".utf8))
        try await Task.sleep(for: .seconds(60))
        return Data()
    }
}

@Test @MainActor func duplicateBuildsAreRejectedAndDifferentDestinationsRunConcurrently() async throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let runner = BlockingRunner()
    let manager = try BuildManager(store: fixture.store, client: XcodeClient(runner: runner))
    let phone = Destination(id: "phone", name: "Phone")
    let first = try await manager.start(
        container: fixture.container, scheme: "App", destination: phone, operation: .test)
    do {
        _ = try await manager.start(container: fixture.container, scheme: "App", destination: phone, operation: .build)
        Issue.record("Duplicate build was accepted")
    } catch is AppError {}
    let second = try await manager.start(
        container: fixture.container, scheme: "App", destination: Destination(id: "other", name: "Other"),
        operation: .build)
    #expect(first != second)
    #expect(manager.records.filter { $0.phase.active }.count == 2)
    #expect(throws: AppError.self) { try manager.clearCache() }
    await manager.shutdown()
    #expect(manager.records.allSatisfy { $0.phase == .cancelled })
    #expect(!manager.active)
}

@Test func commandArgumentsDoNotInvokeAShellAndPreserveTestScope() {
    let container = Container(kind: .workspace, name: "App", path: "/Some App.xcworkspace")
    var record = BuildRecord(
        container: container, scheme: "App $(touch nope)", destination: Destination(id: "phone", name: "Phone"),
        operation: .test, derivedData: "/cache path", logPath: "/log")
    record.coverage = false
    record.testTargets = ["AppTests/Suite/test()"]
    record.resultBundlePath = "/result path/Tests.xcresult"
    let arguments = XcodeClient().arguments(for: record)
    #expect(arguments.contains("App $(touch nope)"))
    #expect(arguments.contains("-only-testing:AppTests/Suite/test()"))
    #expect(arguments.contains("NO"))
    #expect(arguments.last == "test")
    record.operation = .discoverTests
    record.enumerationPath = "/tests.json"
    #expect(XcodeClient().arguments(for: record).contains("-enumerate-tests"))
    #expect(!XcodeClient().arguments(for: record).contains("-resultBundlePath"))
}

@Test func runnerCapturesOutputAndReportsFailure() async throws {
    let runner = CommandRunner()
    #expect(try await runner.run("/bin/echo", ["hello world"]) == Data("hello world\n".utf8))
    do {
        try await runner.run("/bin/sh", ["-c", "echo useful-error >&2; exit 7"])
        Issue.record("Expected command failure")
    } catch { #expect(error.localizedDescription.contains("useful-error")) }
}

@Test func runnerCancellationTerminatesProcessAndDrainsPipe() async throws {
    let started = Mutex(false)
    let task = Task {
        try await CommandRunner().run(
            "/bin/sh", ["-c", "echo ready; sleep 60"], output: { _ in started.withLock { $0 = true } })
    }
    let deadline = Date().addingTimeInterval(5)
    while !started.withLock({ $0 }) && Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
    #expect(started.withLock { $0 })
    let stop = Date()
    task.cancel()
    do {
        _ = try await task.value
        Issue.record("Expected cancellation")
    } catch { #expect(error is CancellationError) }
    #expect(Date().timeIntervalSince(stop) < 5)
}

@Test(arguments: [LazyXcodeCore.Operation.test, .discoverTests]) @MainActor
func simulatorTestsBootAndOpenDeviceBeforeXcodebuild(operation: LazyXcodeCore.Operation) async throws {
    actor Runner: CommandRunning {
        var calls: [[String]] = []
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            calls.append([executable] + arguments)
            if executable == "xcode-select" { return Data("/fixture/Xcode.app/Contents/Developer\n".utf8) }
            return Data()
        }
    }
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let runner = Runner()
    let manager = try BuildManager(store: fixture.store, client: XcodeClient(runner: runner))
    _ = try await manager.start(
        container: fixture.container, scheme: "App",
        destination: Destination(id: "phone", name: "Phone", platform: "iOS Simulator"), operation: operation)
    let deadline = Date().addingTimeInterval(5)
    while manager.active && Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
    #expect(!manager.active)
    let calls = await runner.calls
    let boot = try #require(calls.firstIndex { $0.contains("boot") })
    let open = try #require(calls.firstIndex { $0.first == "open" })
    let ready = try #require(calls.firstIndex { $0.contains("bootstatus") })
    let build = try #require(calls.firstIndex { $0.first == "xcodebuild" })
    #expect(boot < open && open < ready && ready < build)
    #expect(manager.records.first?.phase == .succeeded)
    await manager.shutdown()
}

@Test func commandRunnerConfiguresUnbufferedChildOutput() async throws {
    let output = String(decoding: try await CommandRunner().run("/usr/bin/env", []), as: UTF8.self)
    for key in ["NSUnbufferedIO", "SIMCTL_CHILD_NSUnbufferedIO", "DEVICECTL_CHILD_NSUnbufferedIO"] {
        #expect(output.contains(key + "=YES"))
    }
}

@Test func successfulCommandOutputExcludesStderrWarnings() async throws {
    let data = try await CommandRunner().run("/bin/sh", ["-c", "echo warning >&2; echo '{\"devices\":{}}'"])
    #expect(try JSONValue.decode(data)["devices"] == .object([:]))
}
