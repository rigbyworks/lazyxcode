import Foundation
import Testing

@testable import LazyXcodeCore

@Test(.enabled(if: ProcessInfo.processInfo.environment["LAZYXCODE_RELEASE_SMOKE"] == "1"), .timeLimit(.minutes(20)))
func releaseSmoke() async throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let runner = CommandRunner()
    let repository = URL(fileURLWithPath: #filePath).deletingLastPathComponent().deletingLastPathComponent()
        .deletingLastPathComponent()
    try await runner.run(
        "python3", [repository.appendingPathComponent("scripts/create-smoke-project.py").path, fixture.root.path])
    let client = XcodeClient()
    try await client.checkEnvironment()
    var arguments = ["simctl", "create", "lazyxcode Swift smoke", "com.apple.CoreSimulator.SimDeviceType.iPhone-16"]
    if let runtime = ProcessInfo.processInfo.environment["LAZYXCODE_SMOKE_RUNTIME"] { arguments.append(runtime) }
    let id = String(decoding: try await runner.run("xcrun", arguments), as: UTF8.self).trimmingCharacters(
        in: .whitespacesAndNewlines)
    do {
        try await exerciseSimulator(id: id, root: fixture.root, client: client)
    } catch {
        _ = try? await runner.run("xcrun", ["simctl", "shutdown", id])
        _ = try? await runner.run("xcrun", ["simctl", "delete", id])
        throw error
    }
    _ = try? await runner.run("xcrun", ["simctl", "shutdown", id])
    try await runner.run("xcrun", ["simctl", "delete", id])
}

private func exerciseSimulator(id: String, root: URL, client: XcodeClient) async throws {
    let container = Container(
        kind: .project, name: "Smoke.xcodeproj", path: root.appendingPathComponent("Smoke.xcodeproj").path)
    let target = Destination(id: id, name: "Smoke", platform: "iOS Simulator")
    let schemes = try await client.schemes(container)
    #expect(schemes.contains("Smoke"))
    let targets = try client.testTargets(container, scheme: "Smoke")
    #expect(targets.contains { $0.name == "SmokeTests" && !$0.isUI })
    let destinations = try await client.destinations(container, scheme: "Smoke")
    #expect(destinations.contains { $0.id == id })
    let log = try ActivityLog(url: root.appendingPathComponent("smoke.log"))
    var record = BuildRecord(
        container: container, scheme: "Smoke", destination: target, operation: .build,
        derivedData: root.appendingPathComponent("DerivedData").path,
        logPath: root.appendingPathComponent("smoke.log").path)
    try await client.build(record, log: log)
    let product = try await client.product(record)
    try await client.boot(target)
    try await client.install(product, on: target)
    let launch = Task { try await client.launch(product, on: target, log: log) }
    let deadline = Date().addingTimeInterval(120)
    while !log.snapshot().text.contains("LAZYXCODE_SMOKE_READY") && Date() < deadline {
        try await Task.sleep(for: .milliseconds(100))
    }
    launch.cancel()
    _ = await launch.result
    #expect(log.snapshot().text.contains("LAZYXCODE_SMOKE_READY"))
    record.operation = .discoverTests
    record.enumerationPath = root.appendingPathComponent("tests.json").path
    try await client.build(record, log: log)
    let enumerated = try XcodeClient.enumeratedTests(Data(contentsOf: URL(fileURLWithPath: record.enumerationPath!)))
    #expect(!enumerated.isEmpty)
    record.operation = .test
    record.enumerationPath = nil
    record.resultBundlePath = root.appendingPathComponent("Tests.xcresult").path
    record.coverage = true
    try await client.build(record, log: log)
    let tests = try await client.testResults(record.resultBundlePath!)
    #expect(tests.count == 1)
    #expect(tests.first?.result == "Passed")
    if let test = tests.first {
        #expect(!(try await client.testDetails(record.resultBundlePath!, id: test.id)).isEmpty)
    }
    let coverage = try await client.coverage(record.resultBundlePath!)
    #expect(!coverage["targets"].array.isEmpty)
}
