import Foundation
import LazyXcodeCore

/// `lazyxcode cache`: inspect and clean up managed DerivedData without opening the workspace.
enum CacheCommand {
    static let help = """
        Usage: lazyxcode cache [status | prune | clear [--all]]

          status       Show DerivedData disk use for each project, and free disk space
          prune        Remove deleted projects' and unused DerivedData now, as lazyxcode does at launch
          clear        Clear DerivedData for the project in the current directory
          clear --all  Clear DerivedData for every project

        DerivedData that a running build is using is never removed.
        Set LAZYXCODE_CACHE_LIMIT to a number of gigabytes to change the 50 GB limit.
        """

    /// Writes the result to `output` and returns the exit status.
    static func run(
        _ arguments: [String], cache: BuildCache, directory: URL,
        environment: [String: String] = ProcessInfo.processInfo.environment, output: (String) -> Void
    ) -> Int32 {
        var policy = CachePolicy()
        do { policy = try CachePolicy(environment: environment) } catch { output(error.localizedDescription) }
        let projects = { (try? Container.discover(in: directory)) ?? [] }
        let stores = {
            projects().map { ProjectStore(container: $0, stateRoot: cache.stateRoot, cacheRoot: cache.root) }
        }
        switch arguments {
        case [], ["status"]:
            output(status(cache: cache, policy: policy))
        case ["prune"]:
            let report = cache.prune(policy: policy, keeping: stores().first)
            output(
                report.removed == 0
                    ? "Nothing to remove. \(WorkspaceModel.bytes(report.keptBytes)) of DerivedData is within the "
                        + "\(WorkspaceModel.bytes(policy.limit)) limit."
                    : "Removed \(report.removed) DerivedData \(report.removed == 1 ? "folder" : "folders") and freed "
                        + "\(WorkspaceModel.bytes(report.freedBytes)). \(WorkspaceModel.bytes(report.keptBytes)) remains."
            )
            if report.inUse > 0 { output("Kept \(report.inUse) in use by a build.") }
        case ["clear"], ["clear", "--all"]:
            var report = BuildCache.Report()
            if arguments.count == 2 {
                report = cache.clearAll()
            } else {
                let stores = stores()
                guard !stores.isEmpty else {
                    output("No .xcworkspace or .xcodeproj in the current directory. Use clear --all for every project.")
                    return 1
                }
                for store in stores { report.add(store.clearDerivedData()) }
            }
            output(
                report.removed == 0 && report.inUse == 0
                    ? "No DerivedData to clear." : "Cleared \(WorkspaceModel.bytes(report.freedBytes)) of DerivedData.")
            if report.inUse > 0 { output("Kept \(report.inUse) in use by a build.") }
        case ["--help"], ["-h"]:
            output(help)
        default:
            output("Unknown cache command: \(arguments.joined(separator: " "))\n\n\(help)")
            return 2
        }
        return 0
    }

    static func status(cache: BuildCache, policy: CachePolicy, now: Date = Date()) -> String {
        let usage = cache.usage()
        let total = usage.reduce(0) { $0 + $1.bytes }
        var lines = [
            "DerivedData in \(abbreviate(cache.root.path)): \(WorkspaceModel.bytes(total)) of the \(WorkspaceModel.bytes(policy.limit)) limit"
        ]
        if let free = BuildCache.availableCapacity(at: cache.root) {
            lines.append(
                "Disk: \(WorkspaceModel.bytes(free)) free"
                    + (free < CachePolicy.lowDiskSpace ? ". Run lazyxcode cache prune." : ""))
        }
        let projects = usage.filter { $0.bytes > 0 }
        guard !projects.isEmpty else { return lines.joined(separator: "\n") }
        let dates = RelativeDateTimeFormatter()
        dates.unitsStyle = .short
        let rows = projects.map { project in
            let name =
                project.container.map { abbreviate($0.path) + (project.missing ? " (deleted)" : "") }
                ?? "Unknown project \(project.key)"
            return (
                WorkspaceModel.bytes(project.bytes),
                project.lastUsed.map { dates.localizedString(for: $0, relativeTo: now) } ?? "", name
            )
        }
        let sizeWidth = max(4, rows.map(\.0.count).max() ?? 0)
        let dateWidth = max(9, rows.map(\.1.count).max() ?? 0)
        func row(_ size: String, _ date: String, _ name: String) -> String {
            String(repeating: " ", count: sizeWidth - size.count) + size + "  " + date
                + String(repeating: " ", count: dateWidth - date.count) + "  " + name
        }
        lines += ["", row("SIZE", "LAST USED", "PROJECT")] + rows.map { row($0.0, $0.1, $0.2) }
        return lines.joined(separator: "\n")
    }

    static func abbreviate(_ path: String) -> String {
        let home = FileManager.default.homeDirectoryForCurrentUser.path
        return path == home || path.hasPrefix(home + "/") ? "~" + path.dropFirst(home.count) : path
    }
}
