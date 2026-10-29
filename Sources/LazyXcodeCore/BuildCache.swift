import Darwin
import Foundation
import Synchronization

/// An exclusive claim on a DerivedData slot, held while xcodebuild may write to it.
/// The lock is a file inside the slot, so other lazyxcode processes and pruning skip a slot in use.
/// The kernel drops the lock if the process exits.
public final class SlotLease: Sendable {
    static let fileName = ".lazyxcode-lease"
    /// When this process last released each lease file.
    private static let releases = Mutex<[String: ContinuousClock.Instant]>([:])
    public let url: URL
    private let descriptor: Mutex<Int32>
    private let recordsUse: Bool

    private init(url: URL, descriptor: Int32, recordsUse: Bool) {
        self.url = url
        self.descriptor = Mutex(descriptor)
        self.recordsUse = recordsUse
    }

    /// Returns nil when another activity or process holds the slot.
    /// With `recordsUse`, the lease file's modification time marks when the slot was last used.
    static func acquire(_ url: URL, recordsUse: Bool) throws -> SlotLease? {
        let file = url.appendingPathComponent(fileName)
        var moved = 0
        while moved < 3 {
            try FileManager.default.createDirectory(at: url, withIntermediateDirectories: true)
            let fd = open(file.path, O_RDWR | O_CREAT | O_CLOEXEC, 0o600)
            guard fd >= 0 else { throw AppError("Could not lock \(url.path): \(String(cString: strerror(errno)))") }
            guard flock(fd, LOCK_EX | LOCK_NB) == 0 else {
                let error = errno
                close(fd)
                // A process forked while this process held the lease keeps the lock until it execs.
                // Wait for that instead of moving to another slot and building it from scratch.
                guard error == EINTR || error == EWOULDBLOCK && releasedRecently(file) else { return nil }
                usleep(2_000)
                continue
            }
            // Pruning may have moved the slot away after this file was opened. Lock the file now at the path instead.
            var held = stat()
            var current = stat()
            if fstat(fd, &held) == 0, stat(file.path, &current) == 0, held.st_dev == current.st_dev,
                held.st_ino == current.st_ino
            {
                if recordsUse { futimes(fd, nil) }
                _ = releases.withLock { $0.removeValue(forKey: file.path) }
                return SlotLease(url: url, descriptor: fd, recordsUse: recordsUse)
            }
            close(fd)
            moved += 1
        }
        return nil
    }

    private static func releasedRecently(_ file: URL) -> Bool {
        releases.withLock { $0[file.path].map { ContinuousClock.now - $0 < .seconds(1) } ?? false }
    }

    public func release() {
        descriptor.withLock { fd in
            guard fd >= 0 else { return }
            if recordsUse { futimes(fd, nil) }
            close(fd)
            fd = -1
            Self.releases.withLock { $0[url.appendingPathComponent(Self.fileName).path] = .now }
        }
    }
    deinit { release() }
}

/// Limits for managed DerivedData across every project.
public struct CachePolicy: Sendable, Equatable {
    /// Least recently used slots are removed while the total exceeds this many bytes.
    public var limit: Int64
    public var maxAge: TimeInterval
    /// Slots after the first exist only for concurrent builds, so they expire sooner.
    public var spareSlotAge: TimeInterval
    public init(
        limit: Int64 = 50_000_000_000, maxAge: TimeInterval = 14 * 86_400, spareSlotAge: TimeInterval = 2 * 86_400
    ) {
        self.limit = limit
        self.maxAge = maxAge
        self.spareSlotAge = spareSlotAge
    }
    /// Reads `LAZYXCODE_CACHE_LIMIT`, a number of gigabytes.
    public init(environment: [String: String]) throws {
        self.init()
        guard let value = environment["LAZYXCODE_CACHE_LIMIT"]?.trimmingCharacters(in: .whitespaces), !value.isEmpty
        else { return }
        guard let gigabytes = Double(value), gigabytes.isFinite, gigabytes > 0 else {
            throw AppError("LAZYXCODE_CACHE_LIMIT must be a number of gigabytes, like 80. Using 50 GB.")
        }
        limit = Int64(min(gigabytes, 1_000_000) * 1_000_000_000)
    }
}

/// Managed DerivedData for every project, and the rules that keep it from growing without bound.
public struct BuildCache: Sendable {
    public struct Slot: Sendable {
        public var url: URL
        public var project: String
        /// Nil for a directory from before slots, keyed by scheme and destination.
        public var number: Int?
        public var lastUsed: Date
    }
    public struct Report: Sendable, Equatable {
        public var removed = 0
        public var freedBytes: Int64 = 0
        /// Slots left in place because an activity is using them.
        public var inUse = 0
        public var keptBytes: Int64 = 0
    }
    static let sizeFileName = ".lazyxcode-size"
    public let root: URL
    public let stateRoot: URL
    public init(root: URL = Store.cacheRoot, stateRoot: URL = Store.stateRoot) {
        self.root = root
        self.stateRoot = stateRoot
    }
    var projects: URL { root.appendingPathComponent("projects") }
    var trash: URL { root.appendingPathComponent("trash") }

    /// Removes the caches of deleted projects, slots unused past the policy's ages, and then the least recently used
    /// slots until the total fits the limit. The first slot of `keeping`, the project being opened, always stays.
    public func prune(policy: CachePolicy, keeping: ProjectStore? = nil, now: Date = Date()) -> Report {
        Self.withThrottledDiskIO {
            emptyTrash()
            let kept = keeping?.cache.lastPathComponent
            var report = Report()
            var remaining: [(slot: Slot, bytes: Int64)] = []
            for project in Self.directories(in: projects) {
                if Task.isCancelled { return report }
                let slots = slots(in: project)
                if let container = container(key: project.lastPathComponent), Self.isMissing(container.path) {
                    removeProject(project, slots: slots, report: &report)
                    continue
                }
                for slot in slots {
                    let primary = slot.number == 0 && slot.project == kept
                    let age = now.timeIntervalSince(slot.lastUsed)
                    // Pre-slot directories are replaced, unless an older lazyxcode may still be building into one.
                    let limit = slot.number.map { $0 == 0 ? policy.maxAge : policy.spareSlotAge } ?? 3600
                    if primary || age <= limit || !remove(slot, report: &report) {
                        remaining.append((slot, size(of: slot)))
                    }
                }
            }
            var total = remaining.reduce(0) { $0 + $1.bytes }
            for (slot, bytes) in remaining.sorted(by: { $0.slot.lastUsed < $1.slot.lastUsed }) {
                guard total > policy.limit, !Task.isCancelled else { break }
                if slot.number == 0 && slot.project == kept { continue }
                if remove(slot, report: &report) { total -= bytes }
            }
            report.keptBytes = total
            return report
        }
    }

    /// Removes every slot of one project that no activity is using.
    public func clear(project: URL) -> Report {
        Self.withThrottledDiskIO {
            var report = Report()
            for slot in slots(in: project) { remove(slot, report: &report) }
            emptyTrash()
            return report
        }
    }

    func slots(in project: URL) -> [Slot] {
        Self.directories(in: project.appendingPathComponent("derived-data")).map { url in
            let name = url.lastPathComponent
            return Slot(
                url: url, project: project.lastPathComponent,
                number: name.hasPrefix("slot-") ? Int(name.dropFirst(5)) : nil, lastUsed: Self.lastUsed(url))
        }
    }

    /// The project a cache directory belongs to, when it can be confirmed.
    func container(key: String) -> Container? {
        let candidates: [Container?] = [
            try? Store.read(Container.self, from: projects.appendingPathComponent("\(key)/project.json")),
            try? Store.read(
                ProjectStore.History.self,
                from: stateRoot.appendingPathComponent("projects/\(key)/builds.json")
            ).builds.first?.container,
        ]
        return candidates.compactMap { $0 }.first {
            Store.shortHash($0.kind.rawValue + "\0" + $0.path) == key
        }
    }

    /// True when the project file is gone. A project on an unmounted volume is not missing.
    static func isMissing(_ path: String) -> Bool {
        guard !FileManager.default.fileExists(atPath: path) else { return false }
        let components = URL(fileURLWithPath: path).pathComponents
        if components.count > 2 && components[1] == "Volumes" {
            return FileManager.default.fileExists(atPath: "/Volumes/" + components[2])
        }
        return true
    }

    /// The slot's last use is recorded on its lease file. Pre-slot directories fall back to the files xcodebuild
    /// updates on every build, not the directory itself, which changes when its size is recorded.
    static func lastUsed(_ slot: URL) -> Date {
        if let date = modified(slot.appendingPathComponent(SlotLease.fileName)) { return date }
        return ["Logs/Build", "Build/Intermediates.noindex/XCBuildData"]
            .compactMap { modified(slot.appendingPathComponent($0)) }.max() ?? modified(slot) ?? .distantPast
    }

    /// The slot's size, measured again only after it has been used since the last measurement.
    func size(of slot: Slot) -> Int64 {
        let record = slot.url.appendingPathComponent(Self.sizeFileName)
        if let measured = Self.modified(record), measured > slot.lastUsed,
            let bytes = (try? String(contentsOf: record, encoding: .utf8)).flatMap({ Int64($0) })
        {
            return bytes
        }
        let bytes = Self.allocatedSize(slot.url)
        try? String(bytes).write(to: record, atomically: true, encoding: .utf8)
        return bytes
    }

    @discardableResult
    private func remove(_ slot: Slot, report: inout Report) -> Bool {
        guard Store.contains(slot.url, in: projects) else { return false }
        guard let lease = try? SlotLease.acquire(slot.url, recordsUse: false) else {
            report.inUse += 1
            return false
        }
        let bytes = size(of: slot)
        defer { lease.release() }
        guard discard(slot.url) else { return false }
        report.removed += 1
        report.freedBytes += bytes
        return true
    }

    private func removeProject(_ project: URL, slots: [Slot], report: inout Report) {
        var leases: [SlotLease] = []
        defer { for lease in leases { lease.release() } }
        for slot in slots {
            guard let lease = try? SlotLease.acquire(slot.url, recordsUse: false) else {
                report.inUse += 1
                return
            }
            leases.append(lease)
        }
        let bytes = Self.allocatedSize(project)
        guard discard(project) else { return }
        report.removed += slots.count
        report.freedBytes += bytes
    }

    /// Moves a directory out of the cache in one step, so no build can find it half-deleted, then deletes it.
    /// Anything left in the trash by an interrupted delete is removed on the next prune.
    private func discard(_ url: URL) -> Bool {
        let destination = trash.appendingPathComponent(UUID().uuidString)
        do {
            try FileManager.default.createDirectory(at: trash, withIntermediateDirectories: true)
            try FileManager.default.moveItem(at: url, to: destination)
        } catch { return false }
        try? FileManager.default.removeItem(at: destination)
        return true
    }

    private func emptyTrash() {
        for item in (try? FileManager.default.contentsOfDirectory(at: trash, includingPropertiesForKeys: nil)) ?? [] {
            try? FileManager.default.removeItem(at: item)
        }
    }

    static func directories(in url: URL) -> [URL] {
        ((try? FileManager.default.contentsOfDirectory(
            at: url, includingPropertiesForKeys: [.isDirectoryKey], options: .skipsHiddenFiles)) ?? [])
            .filter { (try? $0.resourceValues(forKeys: [.isDirectoryKey]).isDirectory) == true }
    }

    static func modified(_ url: URL) -> Date? {
        try? url.resourceValues(forKeys: [.contentModificationDateKey]).contentModificationDate
    }

    /// Disk space the files use, rather than their logical length.
    public static func allocatedSize(_ url: URL) -> Int64 {
        let keys: Set<URLResourceKey> = [.totalFileAllocatedSizeKey, .isRegularFileKey]
        var total: Int64 = 0
        let files = FileManager.default.enumerator(at: url, includingPropertiesForKeys: Array(keys))
        while let file = files?.nextObject() as? URL {
            guard let values = try? file.resourceValues(forKeys: keys), values.isRegularFile == true else { continue }
            total += Int64(values.totalFileAllocatedSize ?? 0)
        }
        return total
    }

    /// Runs cache maintenance at background disk priority so it never slows a build.
    static func withThrottledDiskIO<T>(_ body: () -> T) -> T {
        let previous = getiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_THREAD)
        setiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_THREAD, IOPOL_THROTTLE)
        defer { setiopolicy_np(IOPOL_TYPE_DISK, IOPOL_SCOPE_THREAD, previous < 0 ? IOPOL_DEFAULT : previous) }
        return body()
    }
}
