import Foundation
import Observation

@MainActor @Observable
public final class BuildManager {
    public private(set) var records: [BuildRecord]
    public private(set) var lastError = ""
    public let store: ProjectStore
    public let client: XcodeClient
    @ObservationIgnored private var tasks: [String: Task<Void, Never>] = [:]
    @ObservationIgnored private var logs: [String: ActivityLog] = [:]
    @ObservationIgnored private var leases: [String: SlotLease] = [:]
    public init(store: ProjectStore, client: XcodeClient = XcodeClient()) throws {
        self.store = store
        self.client = client
        records = try store.load()
    }
    public var active: Bool { !tasks.isEmpty }
    public func log(_ id: String) -> ActivityLog? { logs[id] }
    public func start(
        container: Container, scheme: String, destination: Destination, operation: Operation,
        targets: [String] = [], scope: String = "All tests", coverage: Bool = true
    ) async throws -> String {
        try Task.checkCancellation()
        while let existing = records.first(where: {
            $0.scheme == scheme && $0.simulator.id == destination.id && $0.phase.active
        }) {
            guard existing.phase == .running, let task = tasks[existing.id] else {
                throw AppError("An activity is already using this scheme and destination")
            }
            task.cancel()
            await task.value
            try Task.checkCancellation()
        }
        let lease = try store.leaseDerivedData()
        let id = UUID().uuidString
        var record = BuildRecord(
            id: id, container: container, scheme: scheme, destination: destination,
            operation: operation, derivedData: lease.url.path, logPath: store.logURL(id: id).path)
        let log: ActivityLog
        do {
            if operation == .test || operation == .discoverTests {
                let directory = try store.resultDirectory(id: id)
                record.testScope = scope
                record.testTargets = targets
                record.coverage = coverage
                if operation == .test {
                    record.resultBundlePath = directory.appendingPathComponent("TestResults.xcresult").path
                } else {
                    record.enumerationPath = directory.appendingPathComponent("tests.json").path
                }
            }
            log = try ActivityLog(url: store.logURL(id: id))
            try store.save([record] + records)
        } catch {
            lease.release()
            throw error
        }
        records.insert(record, at: 0)
        logs[id] = log
        leases[id] = lease
        tasks[id] = Task { await self.execute(record, log: log) }
        return id
    }
    private func execute(_ record: BuildRecord, log: ActivityLog) async {
        defer {
            tasks[record.id] = nil
            logs[record.id] = nil
            leases.removeValue(forKey: record.id)?.release()
            trimHistory()
        }
        do {
            try Task.checkCancellation()
            if (record.operation == .test || record.operation == .discoverTests) && record.simulator.isSimulator {
                setPhase(record.id, .booting)
                try await client.boot(record.simulator)
            }
            setPhase(record.id, record.operation == .test || record.operation == .discoverTests ? .testing : .building)
            try await client.build(record, log: log)
            if record.operation == .run {
                let product = try await client.product(record)
                setPhase(record.id, .booting)
                try await client.boot(record.simulator)
                setPhase(record.id, .installing)
                try await client.install(product, on: record.simulator)
                setPhase(record.id, .launching)
                try Task.checkCancellation()
                setPhase(record.id, .running)
                try await client.launch(product, on: record.simulator, log: log)
            }
            try Task.checkCancellation()
            setPhase(record.id, .succeeded)
        } catch {
            let current = records.first { $0.id == record.id }?.phase
            let phase: Phase =
                Task.isCancelled || error is CancellationError
                ? .cancelled
                : current == .testing ? .testFailed : current == .building ? .buildFailed : .runFailed
            setPhase(record.id, phase, error: phase == .cancelled ? "Cancelled" : error.localizedDescription)
        }
        log.finish()
        if let error = log.snapshot().error { lastError = "Could not persist complete log: \(error)" }
    }
    private func setPhase(_ id: String, _ phase: Phase, error: String? = nil) {
        guard let index = records.firstIndex(where: { $0.id == id }) else { return }
        records[index].phase = phase
        records[index].error = error
        if !phase.active { records[index].finishedAt = Date() }
        // A launched app no longer needs DerivedData, so another build may use the slot.
        if !phase.active || phase == .running { leases.removeValue(forKey: id)?.release() }
        let line = "\n[lazyxcode \(Date().formatted(.iso8601))] \(phase.rawValue)\(error.map { ": " + $0 } ?? "")\n"
        logs[id]?.append(Data(line.utf8))
        do { try store.save(records) } catch { lastError = "Could not save history: \(error.localizedDescription)" }
    }
    private func trimHistory() {
        records = records.enumerated().filter { $0.offset < 100 || $0.element.phase.active }.map(\.element)
    }
    public func cancel(_ id: String) { tasks[id]?.cancel() }
    public func shutdown() async {
        let running = Array(tasks.values)
        for task in running { task.cancel() }
        for task in running { await task.value }
    }
    @discardableResult
    public func clearCache() throws -> BuildCache.Report {
        guard !active else { throw AppError("Stop active activities before clearing DerivedData") }
        return store.clearDerivedData()
    }
}
