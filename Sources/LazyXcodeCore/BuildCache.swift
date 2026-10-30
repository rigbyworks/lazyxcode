import Darwin
import Foundation
import Synchronization

/// An exclusive claim on a DerivedData slot, held while xcodebuild may write to it.
/// The lock is a file inside the slot, so other lazyxcode processes and pruning skip a slot in use.
/// The kernel drops the lock if the process exits.
public final class SlotLease: Sendable {
    static let fileName = ".lazyxcode-lease"
    /// A lease file, identified by device and inode rather than by path, since the same file may be reached as
    /// /var/... or /private/var/....
    private struct FileID: Hashable {
        var device: dev_t
        var inode: ino_t
        init(_ info: stat) {
            device = info.st_dev
            inode = info.st_ino
        }
        init?(descriptor: Int32) {
            var info = stat()
            guard fstat(descriptor, &info) == 0 else { return nil }
            self.init(info)
        }
    }
    /// When this process last released each lease file.
    private static let releases = Mutex<[FileID: ContinuousClock.Instant]>([:])
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
                // A process forked while this process held the lease keeps the lock until it execs.
                // Wait for that instead of moving to another slot and building it from scratch.
                let released = error == EWOULDBLOCK && releasedRecently(fd)
                close(fd)
                guard error == EINTR || released else { return nil }
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
                _ = releases.withLock { $0.removeValue(forKey: FileID(held)) }
                return SlotLease(url: url, descriptor: fd, recordsUse: recordsUse)
            }
            close(fd)
            moved += 1
        }
        return nil
    }

    private static func releasedRecently(_ descriptor: Int32) -> Bool {
        guard let file = FileID(descriptor: descriptor) else { return false }
        return releases.withLock { $0[file].map { ContinuousClock.now - $0 < .seconds(1) } ?? false }
    }

    public func release() {
        descriptor.withLock { fd in
            guard fd >= 0 else { return }
            if recordsUse { futimes(fd, nil) }
            let file = FileID(descriptor: fd)
            close(fd)
            fd = -1
            if let file { Self.releases.withLock { $0[file] = .now } }
        }
    }
    deinit { release() }
}

/// Marks a project as open in a lazyxcode process, so pruning and clearing other projects keep its first slot.
/// Each process holds a shared lock on a file in the project's cache. The kernel drops it if the process exits.
public final class OpenProject: Sendable {
    static let fileName = ".lazyxcode-open"
    private let descriptor: Int32

    init?(cache: URL) {
        try? FileManager.default.createDirectory(at: cache, withIntermediateDirectories: true)
        let fd = open(cache.appendingPathComponent(Self.fileName).path, O_RDONLY | O_CREAT | O_CLOEXEC, 0o600)
        guard fd >= 0 else { return nil }
        // Blocks only for the moment another process holds the lock to check whether the project is open.
        while flock(fd, LOCK_SH) != 0 {
            guard errno == EINTR else {
                close(fd)
                return nil
            }
        }
        descriptor = fd
    }
    deinit { close(descriptor) }

    /// True while any lazyxcode process, including this one, has the project open.
    static func isOpen(cache: URL) -> Bool {
        let fd = open(cache.appendingPathComponent(fileName).path, O_RDONLY | O_CLOEXEC)
        guard fd >= 0 else { return false }
        defer { close(fd) }
        return flock(fd, LOCK_EX | LOCK_NB) != 0 && errno == EWOULDBLOCK
    }
}

/// Limits for managed DerivedData across every project.
public struct CachePolicy: Sendable, Equatable {
    /// Below this much free disk space, lazyxcode asks before starting a build.
    public static let lowDiskSpace: Int64 = 20_000_000_000
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
        /// Slots left in place because a build or a running app is using them.
        public var inUse = 0
        /// First slots left in place because their project is open in a lazyxcode process.
        public var open = 0
        public var keptBytes: Int64 = 0
        public init() {}
        public mutating func add(_ other: Report) {
            removed += other.removed
            freedBytes += other.freedBytes
            inUse += other.inUse
            open += other.open
            keptBytes += other.keptBytes
        }
    }
    public struct ProjectUsage: Sendable {
        public var key: String
        /// Nil when the cache no longer records which project it belongs to.
        public var container: Container?
        public var missing: Bool
        /// True while a lazyxcode process has the project open.
        public var open: Bool
        public var bytes: Int64
        public var slots: Int
        public var lastUsed: Date?
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
    /// slots until the total fits the limit. The first slot of `keeping`, the project being opened, and of every
    /// project open in a lazyxcode process always stays.
    public func prune(policy: CachePolicy, keeping: ProjectStore? = nil, now: Date = Date()) -> Report {
        Self.withThrottledDiskIO {
            emptyTrash()
            let kept = keeping?.cache.lastPathComponent
            var report = Report()
            var remaining: [(slot: Slot, bytes: Int64, pinned: Bool)] = []
            for project in Self.directories(in: projects) {
                if Task.isCancelled { return report }
                let slots = slots(in: project)
                // A deleted project that is open keeps its cache, open marker included, and is pruned like any other.
                if let container = container(key: project.lastPathComponent), Self.isMissing(container.path),
                    !OpenProject.isOpen(cache: project)
                {
                    if removeProject(project, slots: slots, report: &report) { continue }
                    // A build or a running app is using part of it. What stays counts toward the limit.
                    for slot in slots where !remove(slot, keepingIfOpen: false, report: &report) {
                        remaining.append((slot, size(of: slot), true))
                    }
                    continue
                }
                for slot in slots {
                    let age = now.timeIntervalSince(slot.lastUsed)
                    // Pre-slot directories are replaced, unless an older lazyxcode may still be building into one.
                    // Older versions take no lease, so a recent one is kept even when the cache is over its limit.
                    let pinned = slot.number == 0 && slot.project == kept || slot.number == nil && age <= 3600
                    let limit = slot.number.map { $0 == 0 ? policy.maxAge : policy.spareSlotAge } ?? 3600
                    let expired = !pinned && age > limit
                    if expired && remove(slot, keepingIfOpen: true, report: &report) { continue }
                    // An expired slot that could not be removed is in use, so the size pass skips it too.
                    remaining.append((slot, size(of: slot), pinned || expired))
                }
            }
            var total = remaining.reduce(0) { $0 + $1.bytes }
            for (slot, bytes, pinned) in remaining.sorted(by: { $0.slot.lastUsed < $1.slot.lastUsed }) {
                guard total > policy.limit, !Task.isCancelled else { break }
                if pinned { continue }
                if remove(slot, keepingIfOpen: true, report: &report) { total -= bytes }
            }
            report.keptBytes = total
            return report
        }
    }

    /// Removes every slot of one project that no activity or running app is using.
    public func clear(project: URL) -> Report {
        clear(project: project, keepingIfOpen: false)
    }

    /// Removes every project's unused slots, except those of `keeping` and the first slot of each project open in
    /// another lazyxcode process.
    public func clearAll(except keeping: ProjectStore? = nil) -> Report {
        var report = Report()
        for project in Self.directories(in: projects)
        where project.lastPathComponent != keeping?.cache.lastPathComponent {
            report.add(clear(project: project, keepingIfOpen: true))
        }
        return report
    }

    private func clear(project: URL, keepingIfOpen: Bool) -> Report {
        Self.withThrottledDiskIO {
            var report = Report()
            for slot in slots(in: project) { remove(slot, keepingIfOpen: keepingIfOpen, report: &report) }
            emptyTrash()
            return report
        }
    }

    /// DerivedData disk use for each project, largest first.
    public func usage() -> [ProjectUsage] {
        Self.withThrottledDiskIO {
            Self.directories(in: projects).map { project in
                let slots = slots(in: project)
                let container = container(key: project.lastPathComponent)
                return ProjectUsage(
                    key: project.lastPathComponent, container: container,
                    missing: container.map { Self.isMissing($0.path) } ?? false,
                    open: OpenProject.isOpen(cache: project),
                    bytes: slots.reduce(0) { $0 + size(of: $1) }, slots: slots.count,
                    lastUsed: slots.map(\.lastUsed).max())
            }.sorted { $0.bytes > $1.bytes }
        }
    }

    /// DerivedData disk use for one project.
    public func size(project: URL) -> Int64 {
        Self.withThrottledDiskIO { slots(in: project).reduce(0) { $0 + size(of: $1) } }
    }

    /// Free space on the volume that holds `url`, counting space macOS can reclaim, as Finder does.
    public static func availableCapacity(at url: URL) -> Int64? {
        var existing = url
        while !FileManager.default.fileExists(atPath: existing.path) && existing.pathComponents.count > 1 {
            existing.deleteLastPathComponent()
        }
        return (try? existing.resourceValues(forKeys: [.volumeAvailableCapacityForImportantUsageKey]))?
            .volumeAvailableCapacityForImportantUsage
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

    /// With `keepingIfOpen`, the first slot stays while its project is open in a lazyxcode process.
    @discardableResult
    private func remove(_ slot: Slot, keepingIfOpen: Bool, report: inout Report) -> Bool {
        guard Store.contains(slot.url, in: projects) else { return false }
        guard let lease = try? SlotLease.acquire(slot.url, recordsUse: false) else {
            report.inUse += 1
            return false
        }
        defer { lease.release() }
        // Checked just before removal, so a project opened while a long prune runs keeps its first slot.
        if keepingIfOpen && slot.number == 0 && OpenProject.isOpen(cache: projects.appendingPathComponent(slot.project))
        {
            report.open += 1
            return false
        }
        // Checked after leasing, since a Mac app keeps its slot leased until it has launched.
        guard !Self.isRunning(from: slot.url) else {
            report.inUse += 1
            return false
        }
        let bytes = size(of: slot)
        guard discard(slot.url) else { return false }
        report.removed += 1
        report.freedBytes += bytes
        return true
    }

    /// Removes a deleted project's whole cache. Returns false, having removed nothing, while a build or a running app
    /// is using any of it.
    private func removeProject(_ project: URL, slots: [Slot], report: inout Report) -> Bool {
        var leases: [SlotLease] = []
        defer { for lease in leases { lease.release() } }
        for slot in slots {
            guard let lease = try? SlotLease.acquire(slot.url, recordsUse: false) else { return false }
            leases.append(lease)
        }
        guard !Self.isRunning(from: project) else { return false }
        let bytes = Self.allocatedSize(project)
        guard discard(project) else { return false }
        report.removed += slots.count
        report.freedBytes += bytes
        return true
    }

    /// True when a running process's executable is inside `directory`, such as a Mac app opened from DerivedData.
    /// Deleting its bundle would break the resources it loads later.
    static func isRunning(from directory: URL) -> Bool {
        // The kernel reports executable paths with symlinks resolved, such as /private/var for /var.
        guard let resolved = realpath(directory.path, nil) else { return false }
        let prefix = String(cString: resolved) + "/"
        free(resolved)
        var pids = [pid_t](repeating: 0, count: Int(proc_listallpids(nil, 0)) + 64)
        let count = pids.withUnsafeMutableBytes { proc_listallpids($0.baseAddress, Int32($0.count)) }
        var path = [CChar](repeating: 0, count: 4 * Int(MAXPATHLEN))
        for pid in pids.prefix(Int(max(count, 0))) where pid > 0 {
            let length = proc_pidpath(pid, &path, UInt32(path.count))
            if length > 0,
                String(decoding: path.prefix(Int(length)).map { UInt8(bitPattern: $0) }, as: UTF8.self)
                    .hasPrefix(prefix)
            {
                return true
            }
        }
        return false
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
