import Foundation
import LazyXcodeCore
import SwiftTUI
import SwiftTUICLI
import Testing

@testable import LazyXcode

@MainActor private func sampleModel() -> WorkspaceModel {
    let model = WorkspaceModel(containers: [
        Container(kind: .project, name: "Example.xcodeproj", path: "/Example.xcodeproj")
    ])
    model.scheme = "Example"
    model.status = "Ready"
    return model
}

@Test @MainActor func workspaceRendersPanesAtNormalAndNarrowSizes() {
    for (width, height) in [(110, 28), (60, 15), (80, 12)] {
        let model = sampleModel()
        let output = RenderOnce.render(
            WorkspaceView(model: model, live: false).frame(width: width, height: height), width: width,
            environment: ["NO_COLOR": "1"], isStdoutTTY: false)
        #expect(output.contains("[Local]  Cloud [1]"))
        #expect(output.contains("Activity [2]"))
        #expect(output.contains("Output [3]"))
        #expect(output.contains("Example"))
    }
}

@Test @MainActor func keyboardNavigationAndSearchAreScopedToPicker() {
    let model = sampleModel()
    let view = WorkspaceView(model: model, live: false)
    #expect(view.handle(KeyPress(.character("3"))) == .handled)
    #expect(model.pane == 2)
    _ = view.handle(KeyPress(.tab))
    #expect(model.pane == 0)
    _ = view.handle(KeyPress(.tab, modifiers: .shift))
    #expect(model.pane == 2)
    model.showMenu("Example picker", [MenuItem("First") {}, MenuItem("Query") {}])
    #expect(view.handle(KeyPress(.character("q"))) == .handled)
    #expect(model.menu?.filtered.count == 1)
    _ = view.handle(KeyPress(.escape))
    #expect(model.menu == nil)
    #expect(view.handle(KeyPress(.character("q"))) == .ignored)
}

@Test(arguments: [true, false]) @MainActor
func destinationRefreshPreservesOpenPickerSelection(keepSelection: Bool) async throws {
    struct Destinations: CommandRunning {
        let keepSelection: Bool
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            if executable == "xcrun" { return Data(#"{"devices":{}}"#.utf8) }
            let destinations = """
                Available destinations for the scheme:
                  { platform:macOS, id:mac, name:My Mac }
                  { platform:iOS, id:new, name:iPhone A }
                """
            let selected = keepSelection ? "\n  { platform:iOS, id:selected, name:iPhone C }" : ""
            return Data((destinations + selected).utf8)
        }
    }
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "App", path: "/App")],
        client: XcodeClient(runner: Destinations(keepSelection: keepSelection)))
    model.scheme = "App"
    model.destinations = [Destination(id: "selected", name: "iPhone C", platform: "iOS", physical: true)]
    model.destinationID = "selected"
    model.buildRow = 1
    let view = WorkspaceView(model: model, live: false)
    #expect(view.handle(KeyPress(.return)) == .handled)
    model.menu?.query = "iPhone"

    await model.destinationRefresh?.value

    let menu = try #require(model.menu)
    #expect(menu.title == "Destination")
    #expect(menu.query == "iPhone")
    #expect(menu.filtered.map(\.id) == (keepSelection ? ["new", "selected"] : ["new"]))
    #expect(menu.index == (keepSelection ? 1 : 0))
    #expect(model.status == "Ready")
}

@Test @MainActor func emptyFailedTestResultsNeverStartWholeSuite() async throws {
    struct EmptyResults: CommandRunning {
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            Data(#"{"testNodes":[]}"#.utf8)
        }
    }
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "App", path: "/App")], client: XcodeClient(runner: EmptyResults()))
    let record = BuildRecord(
        container: model.container, scheme: "App", destination: Destination(id: "phone", name: "Phone"),
        operation: .test, derivedData: "/cache", logPath: "/log")
    model.rerunFailed(path: "/Tests.xcresult", record: record)
    await model.pending?.value
    #expect(model.status.contains("No failed tests"))
    #expect(model.records.isEmpty)
}

@Test @MainActor func cancelledResultLoadCannotReplaceNewSelection() async throws {
    let model = sampleModel()
    model.load("Loading") {
        try await Task.sleep(for: .seconds(60))
        try Task.checkCancellation()
        model.showDetail("stale")
    }
    let pending = model.pending
    model.selectRecord("new")
    await pending?.value
    #expect(!model.loading)
    #expect(model.detailText == nil)
}

@Test func outputStripsTerminalControlSequencesAndBoundsLines() {
    #expect(OutputFormatter.sanitize("\u{1B}[31merror\u{1B}[0m\u{7}") == "error")
    #expect(OutputFormatter.lines(String(repeating: "line\n", count: 3000)).count == 2000)
    #expect(OutputFormatter.lines(String(repeating: "x", count: 5000))[0].count == 4096)
}

@Test @MainActor func shutdownWaitsForDiscoveryAndCancelsQueuedBuilds() async throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let model = sampleModel()
    let manager = try BuildManager(store: ProjectStore(container: model.container, stateRoot: root, cacheRoot: root))
    model.manager = manager
    model.managers[model.container.path] = manager
    model.destinations = [Destination(id: "fixture", name: "Fixture")]
    model.destinationID = "fixture"
    var discoveryStopped = false
    model.discovery = Task {
        do { try await Task.sleep(for: .seconds(60)) } catch {}
        discoveryStopped = true
    }
    model.queue(.build)
    await model.shutdown()
    #expect(discoveryStopped)
    #expect(manager.records.isEmpty)
    #expect(!manager.active)
}

@Test @MainActor func legacySmallTerminalAndCenteredPickerKeepWorkspaceVisible() {
    for (width, height) in [(44, 10), (74, 23), (120, 36)] {
        let model = sampleModel()
        let layout = WorkspaceLayout(width: width, height: height, pane: 0)
        #expect(layout.sidebar + layout.outputWidth + 2 == width)
        let output = RenderOnce.render(
            WorkspaceView(model: model, live: false).frame(width: width, height: height), width: width,
            environment: ["NO_COLOR": "1"], isStdoutTTY: false)
        #expect(!output.contains("too small"))
        #expect(output.contains("Scheme"))
        #expect(output.contains("Target"))
        #expect(output.contains("[:] Actions"))
        if width == 120 {
            model.showMenu("Choose target", [MenuItem("Phone") {}])
            let picker = RenderOnce.render(
                WorkspaceView(model: model, live: false).frame(width: width, height: height), width: width,
                environment: ["NO_COLOR": "1"], isStdoutTTY: false)
            #expect(picker.contains("Choose target"))
            #expect(picker.contains("[Local]  Cloud [1]"))
        }
    }
}

@Test @MainActor func scrollingPausesOutputUntilFollowIsRequested() {
    let model = sampleModel()
    model.showDetail((0..<100).map { "line \($0)" }.joined(separator: "\n"))
    model.follow = true
    model.outputHeight = 10
    model.move(-1)
    let paused = model.displayedText
    model.detailText = "new live data"
    #expect(model.displayedText == paused)
    model.jump(last: true)
    #expect(model.displayedText == "new live data")
}

@Test @MainActor func modeSwitchRestoresLocalDetailsAndScroll() async {
    let model = sampleModel()
    model.showDetail("Local test details", actions: [MenuItem("Activities") {}])
    model.outputOffset = 7
    model.toggleMode()
    model.showDetail("Cloud details")
    model.toggleMode()
    #expect(model.detailText == "Local test details")
    #expect(model.detailActions.first?.title == "Activities")
    #expect(model.outputOffset == 7)
    #expect(!model.follow)
    await model.shutdown()
}

@Test @MainActor func cacheClearRequiresConfirmationAndConfigRowsWrap() {
    let model = sampleModel()
    let view = WorkspaceView(model: model, live: false)
    _ = view.handle(KeyPress(.character("c")))
    #expect(model.menu?.title == "Clear build cache?")
    #expect(model.menu?.index == 0)
    _ = view.handle(KeyPress(.escape))
    model.buildRow = 0
    model.move(-1)
    #expect(model.buildRow == 1)
    model.move(1)
    #expect(model.buildRow == 0)
    #expect(model.footer(width: 44).contains("[m] Cloud"))
}

@Test func rawPagesCanDisplayEveryLineAndLiveLimitsAreExplicit() {
    let text = (0..<4000).map { "\($0)" }.joined(separator: "\n")
    #expect(OutputFormatter.wrappedLines(text, width: 60, limit: nil).count == 4000)
    #expect(OutputFormatter.rawWindow(text).contains("Earlier output omitted"))
    #expect(OutputFormatter.rawWindow(text).contains("3999"))
}

@Test @MainActor func quitWithActiveWorkRequiresConfirmation() {
    let model = sampleModel()
    let pending = Task<Void, Never> { try? await Task.sleep(for: .seconds(60)) }
    model.queuedRequests[UUID()] = pending
    defer { pending.cancel() }
    let view = WorkspaceView(model: model, live: false)
    #expect(view.handle(KeyPress(.character("q"))) == .handled)
    #expect(model.menu?.title == "Active Activities")
    #expect(model.menu?.searchable == false)
    _ = view.handle(KeyPress(.arrowDown))
    #expect(view.handle(KeyPress(.return)) == .ignored)
    #expect(model.quitRequested)
}

@Test @MainActor func targetPickerStartsAtCurrentDestination() {
    let model = sampleModel()
    model.scheme = ""
    model.destinations = [Destination(id: "one", name: "One"), Destination(id: "two", name: "Two")]
    model.destinationID = "two"
    model.buildRow = 1
    model.activate()
    #expect(model.menu?.index == 1)
    #expect(model.menu?.filtered[1].id == "two")
}

@Test @MainActor func buildOutputSnapshotUsesCompactRowsAndDiagnostics() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let model = sampleModel()
    let store = ProjectStore(container: model.container, stateRoot: root, cacheRoot: root)
    let destination = Destination(id: "phone", name: "iPhone 17 Pro", os: "26.0", platform: "iOS Simulator")
    var record = BuildRecord(
        id: "preview-001", container: model.container, scheme: "Example", destination: destination, operation: .build,
        derivedData: "/cache", logPath: store.logURL(id: "preview-001").path)
    record.phase = .succeeded
    record.startedAt = Date(timeIntervalSince1970: 1000)
    record.finishedAt = Date(timeIntervalSince1970: 1026)
    try store.save([record])
    model.manager = try BuildManager(store: store)
    model.destinations = [destination]
    model.destinationID = destination.id
    model.pane = 2
    var summary = BuildOutput()
    summary.append(
        Data(
            """
            [lazyxcode:step] done 8000 0 Resolve packages
            [lazyxcode:step] done 500 1 Plan build
            [lazyxcode:step] done 13000 4 Compile sources
            [lazyxcode:step] done 1000 3 Compile resources
            [lazyxcode:step] done 2000 5 Link
            [lazyxcode:step] done 1500 7 Sign
            /src/Example.swift:42:7: warning: unused value
            ** BUILD SUCCEEDED **

            """.utf8))
    model.outputSummary = summary
    let output = RenderOnce.render(
        WorkspaceView(model: model, live: false).frame(width: 120, height: 30), width: 120,
        environment: ["NO_COLOR": "1"], isStdoutTTY: false)
    #expect(output.contains("BUILD STEPS"))
    #expect(output.contains("Compile sources"))
    #expect(output.contains("13.0s"))
    #expect(output.contains("DIAGNOSTICS (1)"))
    #expect(output.contains("Example.swift:42:7"))
    #expect(!output.contains("limit reached"))
    if let directory = ProcessInfo.processInfo.environment["LAZYXCODE_SNAPSHOT_DIR"] {
        let directory = URL(fileURLWithPath: directory)
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        try output.write(to: directory.appendingPathComponent("build-output.txt"), atomically: true, encoding: .utf8)
        model.showMenu("Destination", model.destinationItems())
        let picker = RenderOnce.render(
            WorkspaceView(model: model, live: false).frame(width: 120, height: 30), width: 120,
            environment: ["NO_COLOR": "1"], isStdoutTTY: false)
        try picker.write(to: directory.appendingPathComponent("target-picker.txt"), atomically: true, encoding: .utf8)
    }
}

@Test func outputWrappingUsesTerminalCellWidths() {
    #expect(OutputFormatter.wrappedLines("测试文件.swift", width: 4, limit: nil) == ["测试", "文件", ".swi", "ft"])
    #expect(OutputFormatter.wrappedLines("a😀bc", width: 3, limit: nil) == ["a😀", "bc"])
}
