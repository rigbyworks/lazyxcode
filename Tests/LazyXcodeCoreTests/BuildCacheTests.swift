import Foundation
import Synchronization
import Testing

@testable import LazyXcodeCore

private actor InstantRunner: CommandRunning {
    func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data {
        Data()
    }
}

private struct CacheFixture {
    let root: URL
    let cache: BuildCache
    init() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        cache = BuildCache(root: root.appendingPathComponent("cache"), stateRoot: root.appendingPathComponent("state"))
    }
    func remove() { try? FileManager.default.removeItem(at: root) }
    /// A project whose `.xcodeproj` exists unless `existing` is false.
    func project(_ name: String, existing: Bool = true) throws -> ProjectStore {
        let path = root.appendingPathComponent("src/\(name).xcodeproj")
        if existing { try FileManager.default.createDirectory(at: path, withIntermediateDirectories: true) }
        return ProjectStore(
            container: Container(kind: .project, name: name, path: path.path),
            stateRoot: cache.stateRoot, cacheRoot: cache.root)
    }
    /// Creates a used slot whose last use was `age` seconds ago and whose recorded size is `bytes`.
    @discardableResult
    func slot(_ store: ProjectStore, _ number: Int, age: TimeInterval, bytes: Int64 = 1000) throws -> URL {
        try Store.write(store.container, to: store.cache.appendingPathComponent("project.json"))
        let url = store.derivedData(slot: number)
        try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
        let used = Date().addingTimeInterval(-age)
        let lease = url.appendingPathComponent(SlotLease.fileName)
        FileManager.default.createFile(atPath: lease.path, contents: nil)
        try FileManager.default.setAttributes([.modificationDate: used], ofItemAtPath: lease.path)
        let size = url.appendingPathComponent(BuildCache.sizeFileName)
        try String(bytes).write(to: size, atomically: true, encoding: .utf8)
        try FileManager.default.setAttributes([.modificationDate: used.addingTimeInterval(1)], ofItemAtPath: size.path)
        return url
    }
}

private func exists(_ url: URL) -> Bool { FileManager.default.fileExists(atPath: url.path) }

@Test func slotLeasesAreExclusiveAndTheLowestFreeSlotIsReused() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let store = try fixture.project("App")
    let first = try store.leaseDerivedData()
    let second = try store.leaseDerivedData()
    #expect(first.url.path == store.derivedData(slot: 0).path)
    #expect(second.url.path == store.derivedData(slot: 1).path)
    first.release()
    let again = try store.leaseDerivedData()
    #expect(again.url.path == store.derivedData(slot: 0).path)
    again.release()
    second.release()
    let recorded = try Store.read(Container.self, from: store.cache.appendingPathComponent("project.json"))
    #expect(recorded == store.container)
}

@Test func releasedSlotIsReusedWhileProcessesLaunch() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let store = try fixture.project("App")
    // A process launched while a lease is held keeps the lock until it execs, even after the lease is released.
    let launching = Atomic(true)
    let launchers = DispatchGroup()
    for _ in 0..<4 {
        DispatchQueue.global().async(group: launchers) {
            while launching.load(ordering: .relaxed) {
                let process = Process()
                process.executableURL = URL(fileURLWithPath: "/usr/bin/true")
                process.standardOutput = Pipe()
                try? process.run()
                process.waitUntilExit()
            }
        }
    }
    defer {
        launching.store(false, ordering: .relaxed)
        launchers.wait()
    }
    for _ in 0..<1000 {
        let lease = try store.leaseDerivedData()
        usleep(200)
        lease.release()
        let again = try store.leaseDerivedData()
        let slot = again.url.lastPathComponent
        again.release()
        guard slot == "slot-0" else {
            Issue.record("Leased \(slot) although slot-0 was free")
            return
        }
    }
}

@Test @MainActor func sequentialBuildsOnDifferentDestinationsShareOneSlot() async throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let store = try fixture.project("App")
    let manager = try BuildManager(store: store, client: XcodeClient(runner: InstantRunner()))
    for destination in ["phone", "tablet", "mac"] {
        _ = try await manager.start(
            container: store.container, scheme: "App", destination: Destination(id: destination, name: destination),
            operation: .build)
        let deadline = Date().addingTimeInterval(5)
        while manager.active && Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
    }
    #expect(manager.records.allSatisfy { $0.derivedDataKey == store.derivedData(slot: 0).path })
    #expect(fixture.cache.slots(in: store.cache).count == 1)
}

@Test func pruneRemovesDeletedProjectsAndExpiredSlots() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let deleted = try fixture.project("Deleted", existing: false)
    try fixture.slot(deleted, 0, age: 60)
    let current = try fixture.project("Current")
    let currentOld = try fixture.slot(current, 0, age: 30 * 86_400)
    let other = try fixture.project("Other")
    let otherOld = try fixture.slot(other, 0, age: 30 * 86_400)
    let otherSpare = try fixture.slot(other, 1, age: 3 * 86_400)
    let recent = try fixture.project("Recent")
    let recentSlot = try fixture.slot(recent, 0, age: 3 * 86_400)
    let report = fixture.cache.prune(policy: CachePolicy(), keeping: current)
    #expect(!exists(deleted.cache))
    #expect(exists(currentOld))
    #expect(!exists(otherOld))
    #expect(!exists(otherSpare))
    #expect(exists(recentSlot))
    #expect(report.removed == 3)
    #expect(report.freedBytes > 0)
    #expect(report.keptBytes == 2000)
    #expect((try? FileManager.default.contentsOfDirectory(atPath: fixture.cache.trash.path))?.isEmpty ?? true)
}

@Test func pruneRemovesLeastRecentlyUsedSlotsOverTheLimit() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let current = try fixture.project("Current")
    let currentSlot = try fixture.slot(current, 0, age: 9 * 86_400, bytes: 4000)
    let oldest = try fixture.slot(try fixture.project("Oldest"), 0, age: 8 * 86_400, bytes: 3000)
    let older = try fixture.slot(try fixture.project("Older"), 0, age: 7 * 86_400, bytes: 3000)
    let newest = try fixture.slot(try fixture.project("Newest"), 0, age: 86_400, bytes: 3000)
    let report = fixture.cache.prune(policy: CachePolicy(limit: 8000), keeping: current)
    #expect(exists(currentSlot))
    #expect(!exists(oldest))
    #expect(!exists(older))
    #expect(exists(newest))
    #expect(report.keptBytes == 7000)
}

@Test func pruneSkipsSlotsInUseAndRecentlyBuiltLegacyDirectories() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let deleted = try fixture.project("Deleted", existing: false)
    try fixture.slot(deleted, 0, age: 60)
    let busy = try deleted.leaseDerivedData()
    let store = try fixture.project("App")
    let legacy = store.cache.appendingPathComponent("derived-data/0123456789abcdef")
    let active = store.cache.appendingPathComponent("derived-data/fedcba9876543210")
    for url in [legacy, active] {
        try FileManager.default.createDirectory(
            at: url.appendingPathComponent("Logs/Build"), withIntermediateDirectories: true)
    }
    let stale = Date().addingTimeInterval(-2 * 3600)
    for path in [legacy.path, legacy.appendingPathComponent("Logs/Build").path] {
        try FileManager.default.setAttributes([.modificationDate: stale], ofItemAtPath: path)
    }
    let report = fixture.cache.prune(policy: CachePolicy(), keeping: store)
    #expect(exists(busy.url))
    #expect(report.inUse == 1)
    #expect(!exists(legacy))
    #expect(exists(active))
    busy.release()
    #expect(fixture.cache.prune(policy: CachePolicy()).removed == 1)
    #expect(!exists(deleted.cache))
}

@Test func slotSizeIsMeasuredOnDiskAndRemeasuredOnlyAfterUse() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let store = try fixture.project("App")
    let lease = try store.leaseDerivedData()
    try Data(count: 100_000).write(to: lease.url.appendingPathComponent("Build.o"))
    lease.release()
    let slot = try #require(fixture.cache.slots(in: store.cache).first)
    let measured = fixture.cache.size(of: slot)
    #expect(measured >= 100_000)
    try Data(count: 100_000).write(to: lease.url.appendingPathComponent("Other.o"))
    #expect(fixture.cache.size(of: slot) == measured)
    try store.leaseDerivedData().release()
    let used = try #require(fixture.cache.slots(in: store.cache).first)
    #expect(fixture.cache.size(of: used) >= 200_000)
}

@Test func cachePolicyReadsTheLimitInGigabytes() throws {
    #expect(try CachePolicy(environment: [:]).limit == 50_000_000_000)
    #expect(try CachePolicy(environment: ["LAZYXCODE_CACHE_LIMIT": " 12.5 "]).limit == 12_500_000_000)
    for value in ["lots", "0", "-3", "inf"] {
        #expect(throws: AppError.self) { try CachePolicy(environment: ["LAZYXCODE_CACHE_LIMIT": value]) }
    }
}

@Test func projectsOnUnmountedVolumesAreNotTreatedAsDeleted() {
    #expect(!BuildCache.isMissing("/Volumes/lazyxcode-not-mounted-\(UUID().uuidString)/App.xcodeproj"))
    #expect(BuildCache.isMissing("/private/tmp/lazyxcode-deleted-\(UUID().uuidString)/App.xcodeproj"))
    #expect(!BuildCache.isMissing("/"))
}

@Test func pruneKeepsRecentlyBuiltLegacyDirectoriesOverTheLimit() throws {
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let store = try fixture.project("App")
    let legacy = store.cache.appendingPathComponent("derived-data/fedcba9876543210")
    let logs = legacy.appendingPathComponent("Logs/Build")
    try FileManager.default.createDirectory(at: logs, withIntermediateDirectories: true)
    try Data(count: 100_000).write(to: logs.appendingPathComponent("Build.xcactivitylog"))
    let report = fixture.cache.prune(policy: CachePolicy(limit: 0))
    #expect(exists(legacy))
    #expect(report.removed == 0)
}

@Test @MainActor func macAppKeepsItsSlotLeasedUntilItHasLaunched() async throws {
    actor Runner: CommandRunning {
        let slot: URL
        var leasedAtLaunch: Bool?
        init(slot: URL) { self.slot = slot }
        func run(_ executable: String, _ arguments: [String], output: (@Sendable (Data) -> Void)?) async throws -> Data
        {
            if arguments.contains("-showBuildSettings") {
                let settings = [
                    "WRAPPER_EXTENSION": "app", "PRODUCT_BUNDLE_IDENTIFIER": "com.example.App",
                    "FULL_PRODUCT_NAME": "App.app", "TARGET_BUILD_DIR": slot.appendingPathComponent("Build").path,
                ]
                return try JSONSerialization.data(withJSONObject: [["buildSettings": settings]])
            }
            if executable == "open" {
                let lease = try SlotLease.acquire(slot, recordsUse: false)
                leasedAtLaunch = lease == nil
                lease?.release()
            }
            return Data()
        }
    }
    let fixture = try CacheFixture()
    defer { fixture.remove() }
    let store = try fixture.project("App")
    let runner = Runner(slot: store.derivedData(slot: 0))
    let manager = try BuildManager(store: store, client: XcodeClient(runner: runner))
    _ = try await manager.start(
        container: store.container, scheme: "App", destination: Destination(id: "mac", name: "Mac", platform: "macOS"),
        operation: .run)
    let deadline = Date().addingTimeInterval(5)
    while manager.active && Date() < deadline { try await Task.sleep(for: .milliseconds(10)) }
    #expect(manager.records.first?.phase == .succeeded)
    #expect(await runner.leasedAtLaunch == true)
    let lease = try #require(try SlotLease.acquire(store.derivedData(slot: 0), recordsUse: false))
    lease.release()
}
