import Foundation
import LazyXcodeCore
import SwiftTUI
import SwiftTUICLI
import Testing

@testable import LazyXcode

private actor DiscoveryRunner: CommandRunning {
    var destinationCalls = 0
    var schemeCalls = 0
    let fail: Bool
    let holdSchemes: Bool
    private var released = false
    private var schemeRelease: CheckedContinuation<Void, Never>?
    init(fail: Bool = false, holdSchemes: Bool = false) {
        self.fail = fail
        self.holdSchemes = holdSchemes
    }
    func releaseSchemes() {
        released = true
        schemeRelease?.resume()
        schemeRelease = nil
    }
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        if fail { throw AppError("Discovery unavailable") }
        if executable == "xcrun" { return Data(#"{"devices":{}}"#.utf8) }
        if arguments.contains("-list") {
            schemeCalls += 1
            if holdSchemes && !released {
                await withCheckedContinuation { schemeRelease = $0 }
            }
            return Data(#"{"project":{"schemes":["App","New"]}}"#.utf8)
        }
        destinationCalls += 1
        return Data("Available destinations for the scheme:\n  { platform:macOS, id:live, name:My Mac }".utf8)
    }
}

@Test @MainActor func cachedDiscoveryAppearsImmediatelyThenRefreshesAndPickerReusesResults() async throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let container = Container(kind: .project, name: "App", path: root.appendingPathComponent("App.xcodeproj").path)
    let store = ProjectStore(container: container, stateRoot: root, cacheRoot: root)
    let cache = DiscoveryCache(store: store)
    cache.saveSchemes(["App"])
    cache.saveDestinations([Destination(id: "cached", name: "Cached Mac", platform: "macOS")], scheme: "App")
    let runner = DiscoveryRunner()
    let model = WorkspaceModel(containers: [container], client: XcodeClient(runner: runner))
    model.manager = try BuildManager(store: store)

    model.reload(useCache: true)
    #expect(model.schemes == ["App"])
    #expect(model.scheme == "App")
    #expect(model.destination?.id == "cached")
    model.activate()
    #expect(model.menu?.filtered.map(\.id) == ["App"])
    await model.discovery?.value
    await model.destinationRefresh?.value
    #expect(model.schemes == ["App", "New"])
    #expect(model.menu?.filtered.map(\.id) == ["App", "New"])
    model.closeMenu()
    #expect(model.destination?.id == "live")
    #expect(cache.destinations(scheme: "App")?.map(\.id) == ["live"])
    #expect(await runner.destinationCalls == 1)

    model.buildRow = 1
    model.activate()
    #expect(model.destinationRefresh == nil)
    #expect(await runner.destinationCalls == 1)
    model.closeMenu()
    model.reload()
    await model.discovery?.value
    await model.destinationRefresh?.value
    #expect(await runner.destinationCalls == 2)
    #expect(await runner.schemeCalls == 2)

    model.lastDestinationRefresh = Date().addingTimeInterval(-16)
    model.refreshDestinations()
    await model.destinationRefresh?.value
    #expect(await runner.destinationCalls == 3)
    await model.shutdown()
}

@Test @MainActor func coldDiscoveryRetainsRememberedDestinationUntilLiveResultsArrive() async throws {
    let runner = DiscoveryRunner()
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "App", path: "/DiscoveryTestApp")],
        client: XcodeClient(runner: runner))
    model.preferences.simulators[model.container.path + "\0App"] = "live"
    model.reload()
    await model.discovery?.value
    await model.destinationRefresh?.value
    #expect(model.scheme == "App")
    #expect(model.destinationID == "live")
    #expect(await runner.destinationCalls == 1)
    await model.shutdown()
}

@Test @MainActor func failedBackgroundDiscoveryPreservesCachedChoices() async throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let container = Container(kind: .project, name: "App", path: root.appendingPathComponent("App.xcodeproj").path)
    let store = ProjectStore(container: container, stateRoot: root, cacheRoot: root)
    let cache = DiscoveryCache(store: store)
    cache.saveSchemes(["App"])
    cache.saveDestinations([Destination(id: "cached", name: "My Mac", platform: "macOS")], scheme: "App")
    let model = WorkspaceModel(containers: [container], client: XcodeClient(runner: DiscoveryRunner(fail: true)))
    model.manager = try BuildManager(store: store)
    model.reload(useCache: true)
    await model.discovery?.value
    await model.destinationRefresh?.value
    #expect(model.schemes == ["App"])
    #expect(model.destination?.id == "cached")
    #expect(model.status.contains("Discovery unavailable"))
    #expect(cache.destinations(scheme: "App")?.map(\.id) == ["cached"])
    await model.shutdown()
}

@Test(arguments: ["App", "Removed"]) @MainActor
func rememberedSchemeStartsDestinationsBeforeSchemeListFinishes(remembered: String) async {
    let runner = DiscoveryRunner(holdSchemes: true)
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "App", path: "/DiscoveryTestApp")],
        client: XcodeClient(runner: runner))
    model.preferences.schemes[model.container.path] = remembered
    model.preferences.simulators[model.container.path + "\0" + remembered] = "live"
    model.reload(useCache: true)
    #expect(model.destinationRefresh != nil)
    await model.destinationRefresh?.value
    #expect(model.destination?.id == "live")
    #expect(model.schemes.isEmpty)
    #expect(model.status == "Loading schemes...")
    await runner.releaseSchemes()
    await model.discovery?.value
    await model.destinationRefresh?.value
    #expect(model.scheme == "App")
    #expect(await runner.destinationCalls == (remembered == "App" ? 1 : 2))
    await model.shutdown()
}

@Test(arguments: [false, true]) @MainActor
func discoveryShowsBrailleSpinnerUntilSuccessOrFailure(fail: Bool) async {
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "App", path: "/DiscoveryTestApp")],
        client: XcodeClient(runner: DiscoveryRunner(fail: fail)))
    func render() -> String {
        RenderOnce.render(
            WorkspaceView(model: model, live: false).frame(width: 80, height: 20), width: 80,
            environment: ["NO_COLOR": "1", "LANG": "en_US.UTF-8"], isStdoutTTY: false)
    }
    model.reload()
    #expect(render().contains("⠷"))
    #expect(render().contains("Loading"))
    model.activate()
    #expect(render().contains("Loading schemes"))
    #expect(!render().contains("No matches"))
    await model.discovery?.value
    await model.destinationRefresh?.value
    #expect(!render().contains("⠷"))
    model.closeMenu()
    model.chooseScheme("New")
    #expect(render().contains("⠷"))
    #expect(render().contains("Loading destinations"))
    await model.destinationRefresh?.value
    #expect(!render().contains("⠷"))
    #expect(model.status == (fail ? "Destination refresh failed: Discovery unavailable" : "Ready"))
    await model.shutdown()
}

@Test @MainActor func reselectingCurrentSchemeReusesFreshDestinations() async {
    let runner = DiscoveryRunner()
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "App", path: "/DiscoveryTestApp")],
        client: XcodeClient(runner: runner))
    model.chooseScheme("App")
    await model.destinationRefresh?.value
    model.chooseScheme("App")
    #expect(model.destination?.id == "live")
    #expect(model.destinationRefresh == nil)
    await model.destinationRefresh?.value
    #expect(await runner.destinationCalls == 1)
    await model.shutdown()
}

@Test @MainActor func discoveryCancellationClearsBothLoadingIndicators() async {
    struct WaitingRunner: CommandRunning {
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            try await Task.sleep(for: .seconds(60))
            return Data()
        }
    }
    let model = WorkspaceModel(
        containers: [Container(kind: .project, name: "App", path: "/DiscoveryTestApp")],
        client: XcodeClient(runner: WaitingRunner()))
    model.preferences.schemes[model.container.path] = "App"
    model.reload()
    #expect(model.discovery != nil)
    #expect(model.destinationRefresh != nil)
    await model.shutdown()
    #expect(model.discoveryStatus == nil)
}
