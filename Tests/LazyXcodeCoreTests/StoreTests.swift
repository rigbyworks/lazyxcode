import Foundation
import Testing

@testable import LazyXcodeCore

struct TemporaryProject {
    let root: URL
    let container: Container
    let store: ProjectStore
    init() throws {
        root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
        try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
        container = Container(
            kind: .project, name: "App.xcodeproj", path: root.appendingPathComponent("App.xcodeproj").path)
        store = ProjectStore(
            container: container, stateRoot: root.appendingPathComponent("state"),
            cacheRoot: root.appendingPathComponent("cache"))
    }
    func remove() { try? FileManager.default.removeItem(at: root) }
    func record(id: String = UUID().uuidString) -> BuildRecord {
        BuildRecord(
            id: id, container: container, scheme: "App", destination: Destination(id: "phone", name: "Phone"),
            operation: .build, derivedData: store.derivedData(scheme: "App", destination: "phone").path,
            logPath: store.logURL(id: id).path)
    }
}

@Test func readsLegacyPreferencesWithMissingOptionalMaps() throws {
    let data = Data(
        #"{"version":1,"containers":{"/code":"/code/App.xcodeproj"},"schemes":{"/code/App.xcodeproj":"App"},"simulators":{"/code/App.xcodeproj\u0000App":"phone"}}"#
            .utf8)
    let preferences = try JSONDecoder().decode(Preferences.self, from: data)
    #expect(preferences.version == 2)
    #expect(preferences.simulators["/code/App.xcodeproj\0App"] == "phone")
    #expect(preferences.coverageDisabled.isEmpty)
}

@Test func legacyHistoryDatesAndInterruptedActivitiesRecover() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    var record = fixture.record()
    record.phase = .running
    try fixture.store.save([record])
    let history = fixture.store.state.appendingPathComponent("builds.json")
    var data = try String(contentsOf: history, encoding: .utf8)
    data = data.replacingOccurrences(
        of: #"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z"#, with: "2026-09-01T12:00:00.123456789Z",
        options: .regularExpression)
    try data.write(to: history, atomically: true, encoding: .utf8)
    let restored = try fixture.store.load()
    #expect(restored[0].phase == .cancelled)
    #expect(restored[0].finishedAt != nil)
    #expect(restored[0].startedAt < Date())
}

@Test func cacheClearingPreservesLogsAndResults() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let record = fixture.record()
    let log = try ActivityLog(url: URL(fileURLWithPath: record.logPath))
    log.append(Data("complete transcript".utf8))
    let result = try fixture.store.resultDirectory(id: record.id)
    try FileManager.default.createDirectory(atPath: record.derivedDataKey, withIntermediateDirectories: true)
    try fixture.store.clearDerivedData()
    #expect(FileManager.default.fileExists(atPath: record.logPath))
    #expect(FileManager.default.fileExists(atPath: result.path))
    #expect(!FileManager.default.fileExists(atPath: record.derivedDataKey))
}

@Test func logPagingAndLiveLimitsPreserveFullTranscript() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let record = fixture.record()
    let log = try ActivityLog(url: URL(fileURLWithPath: record.logPath))
    let data = Data(String(repeating: "0123456789\n", count: 100_000).utf8)
    log.append(data)
    #expect(log.snapshot().text.utf8.count <= 256 * 1024)
    #expect(try Data(contentsOf: URL(fileURLWithPath: record.logPath)) == data)
    let latest = try fixture.store.logPage(record, size: 100)
    let older = try fixture.store.logPage(record, end: latest.start, size: 100)
    #expect(older.end == latest.start)
    #expect(latest.text.utf8.count == 100)
    var escaped = record
    escaped.logPath = "/etc/passwd"
    #expect(throws: AppError.self) { try fixture.store.logPage(escaped) }
}

@Test func historyRetentionKeepsActiveRecords() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    var records = (0..<103).map { index in
        var record = fixture.record(id: "activity-\(index)")
        record.phase = .succeeded
        record.startedAt = Date(timeIntervalSince1970: Double(index))
        return record
    }
    records[0].phase = .building
    try fixture.store.save(records)
    let saved = try Store.read(
        ProjectStore.History.self, from: fixture.store.state.appendingPathComponent("builds.json"))
    #expect(saved.builds.count == 101)
    #expect(saved.builds.contains { $0.id == "activity-0" })
}

@Test func managedPathsRejectEscapesAndKeepLegacyHash() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    #expect(Store.shortHash("abc") == "ba7816bf8f01cfea")
    #expect(throws: AppError.self) { try fixture.store.resultDirectory(id: "../escape") }
    let file = try fixture.store.artifactURL(run: "../../run", artifact: "../../artifact", name: "../../secret.zip")
    #expect(Store.contains(file, in: fixture.store.cache))
    #expect(file.lastPathComponent == "secret.zip")
}
