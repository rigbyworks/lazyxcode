import Foundation
import LazyXcodeCore
import Testing

@testable import LazyXcode

private actor InstantRunner: CommandRunning {
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        Data()
    }
}

/// A cache in a temporary directory with an existing project, so nothing touches the real cache.
private struct CacheFixture {
    let root: URL
    let cache: BuildCache
    init() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        cache = BuildCache(root: root.appendingPathComponent("cache"), stateRoot: root.appendingPathComponent("state"))
    }
    func remove() { try? FileManager.default.removeItem(at: root) }
    func store(_ name: String) throws -> ProjectStore {
        let directory = root.appendingPathComponent(name)
        try FileManager.default.createDirectory(
            at: directory.appendingPathComponent("\(name).xcodeproj"), withIntermediateDirectories: true)
        // Discovered as lazyxcode does, so the path resolves /var to /private/var the same way.
        return ProjectStore(
            container: try #require(try Container.discover(in: directory).first), stateRoot: cache.stateRoot,
            cacheRoot: cache.root)
    }
    /// Builds once into the project's first slot and leaves `bytes` of output in it.
    @discardableResult
    func build(_ store: ProjectStore, bytes: Int = 100_000) throws -> URL {
        let lease = try store.leaseDerivedData()
        defer { lease.release() }
        try Data(count: bytes).write(to: lease.url.appendingPathComponent("Build.o"))
        return lease.url
    }
    @MainActor func model(_ store: ProjectStore) throws -> WorkspaceModel {
        let model = WorkspaceModel(containers: [store.container], buildCache: cache)
        let manager = try BuildManager(store: store, client: XcodeClient(runner: InstantRunner()))
        model.manager = manager
        model.managers[store.container.path] = manager
        model.scheme = "App"
        model.destinations = [Destination(id: "phone", name: "Phone")]
        model.destinationID = "phone"
        model.status = "Ready"
        return model
    }
}

@MainActor private func waitUntil(_ condition: () -> Bool) async throws {
    // UI tests share the main actor with slow renders.
    let deadline = Date().addingTimeInterval(30)
    while !condition() && Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
    #expect(condition())
}

@MainActor private func choose(_ model: WorkspaceModel, _ prefix: String) throws {
    let item = try #require(model.menu?.items.first { $0.title.hasPrefix(prefix) })
    item.action()
}

private func exists(_ url: URL) -> Bool { FileManager.default.fileExists(atPath: url.path) }

@Test @MainActor func cacheMenuClearsOtherProjectsInTheBackground() async throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let current = try fixture.store("Current")
    let currentSlot = try fixture.build(current)
    let otherSlot = try fixture.build(try fixture.store("Other"), bytes: 300_000)
    let model = try fixture.model(current)
    model.refreshCacheSize()
    try await waitUntil { model.cacheTotalBytes != nil }
    #expect(model.cacheBytes >= 100_000)
    #expect(model.cacheTotalBytes! >= 400_000)
    #expect(BuildPaneState(model: model).cacheDetail.contains(" of "))
    model.requestClearCache()
    #expect(model.menu?.items.map(\.title).last?.hasPrefix("Clear other projects' DerivedData") == true)
    try choose(model, "Clear other projects")
    try await waitUntil { model.status.hasPrefix("Cleared") }
    #expect(!exists(otherSlot))
    #expect(exists(currentSlot))
    try await waitUntil { model.cacheTotalBytes == model.cacheBytes }
    model.requestClearCache()
    #expect(model.menu?.items.count == 2)
}

@Test @MainActor func lowDiskSpaceAsksOnceBeforeStartingAnActivity() async throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let current = try fixture.store("Current")
    let model = try fixture.model(current)
    model.lowSpaceThreshold = .max
    model.queue(.build)
    #expect(model.menu?.title.hasPrefix("Only ") == true)
    try choose(model, "Cancel")
    #expect(model.menu == nil)
    #expect(model.records.isEmpty)
    model.queue(.build)
    try choose(model, "Continue anyway")
    try await waitUntil { model.records.count == 1 && model.manager?.active == false }
    model.queue(.build)
    #expect(model.menu == nil)
    try await waitUntil { model.records.count == 2 }
    await model.shutdown()
}

@Test @MainActor func lowDiskSpaceCleanupStartsTheActivityThatWasRequested() async throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let current = try fixture.store("Current")
    let model = try fixture.model(current)
    model.destinations.append(Destination(id: "pad", name: "Pad"))
    model.lowSpaceThreshold = .max
    for switchesProject in [false, true] {
        try fixture.build(try fixture.store("Other"))
        model.lowSpaceAcknowledged = false
        model.refreshCacheSize()
        try await waitUntil { model.otherProjectsBytes != nil }
        model.queue(.build)
        try choose(model, "Clear other projects")
        // The selection changes while other projects' DerivedData is still being cleared.
        model.scheme = "Changed"
        model.destinationID = "pad"
        if switchesProject { model.generation = UUID() }
        try await waitUntil { model.cacheClearing == nil && model.queuedRequests.isEmpty }
        try await waitUntil { model.manager?.active == false }
        model.scheme = "App"
        model.destinationID = "phone"
    }
    #expect(model.records.count == 1)
    #expect(model.records.first?.scheme == "App")
    #expect(model.records.first?.simulator.id == "phone")
    await model.shutdown()
}

@Test @MainActor func lowDiskSpaceShowsAsAWarningInTheBuildPane() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let model = try fixture.model(try fixture.store("Current"))
    model.cacheBytes = 4_200_000_000
    model.cacheTotalBytes = 18_000_000_000
    model.freeBytes = 100_000_000_000
    var state = BuildPaneState(model: model)
    #expect(state.cacheDetail == "Cache 4.2 GB of 18 GB · [c]")
    #expect(!state.cacheWarning)
    model.freeBytes = 8_000_000_000
    state = BuildPaneState(model: model)
    #expect(state.cacheDetail == "Disk 8 GB free · [c] Clear")
    #expect(state.cacheWarning)
}

@Test func cacheCommandReportsPrunesAndClears() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let current = try fixture.store("Current")
    let currentSlot = try fixture.build(current)
    let other = try fixture.store("Other")
    let otherSlot = try fixture.build(other)
    try FileManager.default.removeItem(atPath: other.container.path)
    func run(_ arguments: [String], in directory: URL? = nil, environment: [String: String] = [:]) -> (
        Int32, String
    ) {
        var lines: [String] = []
        let status = CacheCommand.run(
            arguments, cache: fixture.cache, directory: directory ?? fixture.root, environment: environment
        ) { lines.append($0) }
        return (status, lines.joined(separator: "\n"))
    }
    let status = run(["status"], environment: ["LAZYXCODE_CACHE_LIMIT": "12"])
    #expect(status.0 == 0)
    #expect(status.1.contains("of the 12 GB limit"))
    #expect(status.1.contains("Current.xcodeproj"))
    #expect(status.1.contains("Other.xcodeproj (deleted)"))
    let prune = run(["prune"])
    #expect(prune.1.hasPrefix("Removed 1 DerivedData folder"))
    #expect(!exists(otherSlot))
    #expect(run(["prune"]).1.hasPrefix("Nothing to remove"))
    #expect(
        run(["clear"], in: fixture.root) == (
            1, "No .xcworkspace or .xcodeproj in the current directory. Use clear --all for every project."
        ))
    #expect(run(["clear"], in: current.container.path.deletingLastPathComponentURL).1.hasPrefix("Cleared"))
    #expect(!exists(currentSlot))
    try fixture.build(current)
    #expect(run(["clear", "--all"]).1.hasPrefix("Cleared"))
    #expect(!exists(currentSlot))
    #expect(run(["clear", "--all"]).1 == "No DerivedData to clear.")
    #expect(run(["bogus"]).0 == 2)
    #expect(
        run(["status"], environment: ["LAZYXCODE_CACHE_LIMIT": "lots"]).1.hasPrefix("LAZYXCODE_CACHE_LIMIT must be"))
}

@Test func cacheCommandSaysWhyDerivedDataWasKept() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let open = try fixture.store("Open")
    let openSlot = try fixture.build(open)
    let marker = open.markOpen()
    defer { _ = marker }
    let busy = try fixture.store("Busy")
    try fixture.build(busy)
    let lease = try busy.leaseDerivedData()
    defer { lease.release() }
    var lines: [String] = []
    _ = CacheCommand.run(["clear", "--all"], cache: fixture.cache, directory: fixture.root, environment: [:]) {
        lines.append($0)
    }
    #expect(
        lines == [
            "Cleared Zero KB of DerivedData.", "Kept 1 in use by a build or a running app.",
            "Kept DerivedData of 1 project open in lazyxcode.",
        ])
    #expect(exists(openSlot))
    #expect(CacheCommand.status(cache: fixture.cache, policy: CachePolicy()).contains("Open.xcodeproj (open)"))
}

extension String {
    fileprivate var deletingLastPathComponentURL: URL { URL(fileURLWithPath: self).deletingLastPathComponent() }
}
