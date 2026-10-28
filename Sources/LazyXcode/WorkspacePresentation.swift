import Foundation
import LazyXcodeCore

struct WorkspaceLayout {
    let width: Int
    let height: Int
    let pane: Int
    var compact: Bool { width < 80 }
    var sidebar: Int { compact ? 0 : min(44, max(30, width / 3)) }
    var contentHeight: Int { max(2, height - (compact ? 4 : 3)) }
    var buildHeight: Int {
        if compact { return contentHeight }
        return contentHeight >= 16 ? 10 : pane == 0 ? max(5, contentHeight - 3) : 3
    }
    var outputWidth: Int { max(1, width - sidebar - 4) }
    var outputHeight: Int { max(1, contentHeight - 2) }
}

// Capture the Build pane's observable reads at the workspace boundary. Passing
// values keeps retained child views sensitive to selection and focus changes.
struct BuildPaneState: Equatable {
    struct Setting: Equatable {
        let label: String
        let value: String
        var busy = false
    }
    let cloudMode: Bool
    let focused: Bool
    let selectedRow: Int
    let settings: [Setting]
    let destinationDetail: String
    let cacheDetail: String
    var cacheWarning = false

    @MainActor init(model: WorkspaceModel) {
        cloudMode = model.cloudMode
        focused = model.pane == 0
        selectedRow = model.buildRow
        if cloudMode {
            settings = [
                Setting(
                    label: "Product",
                    value: model.cloudProducts.first { $0.id == model.cloudProduct }?.name ?? "Choose product…"),
                Setting(
                    label: "Workflow",
                    value: model.cloudWorkflows.first { $0.id == model.cloudWorkflow }?.name ?? "All workflows"),
                Setting(label: "Connection", value: model.cloudRefreshing ? "Refreshing…" : "Details"),
            ]
            destinationDetail = ""
            cacheDetail = ""
        } else {
            settings = [
                Setting(
                    label: "Scheme",
                    value: model.scheme.isEmpty
                        ? (model.discovery != nil ? "Loading…" : "Choose a scheme…") : model.scheme,
                    busy: model.discovery != nil),
                Setting(
                    label: "Target",
                    value: model.destination?.name
                        ?? (model.destinationRefresh != nil ? "Loading…" : "Choose a destination…"),
                    busy: model.destinationRefresh != nil),
            ]
            destinationDetail =
                model.destination.map { [$0.platform, $0.os].filter { !$0.isEmpty }.joined(separator: " · ") }
                ?? "[R] Refresh destinations"
            let project = WorkspaceModel.bytes(model.cacheBytes)
            if let free = model.freeBytes, free < model.lowSpaceThreshold {
                cacheDetail = "Disk \(WorkspaceModel.bytes(free)) free · [c] Clear"
                cacheWarning = true
            } else if let total = model.cacheTotalBytes, total > model.cacheBytes {
                cacheDetail = "Cache \(project) of \(WorkspaceModel.bytes(total)) · [c]"
            } else {
                cacheDetail = "Cache \(project) · [c] Clear"
            }
        }
    }
}

extension WorkspaceModel {
    var hasActiveActivities: Bool {
        managers.values.contains(where: \.active) || manager?.active == true || !queuedRequests.isEmpty
    }
    func requestQuit() -> Bool {
        guard hasActiveActivities || simulatorCreation != nil else { return true }
        showMenu(
            "Active Activities",
            [
                MenuItem("Keep working") { self.closeMenu() },
                MenuItem("Cancel activities and quit") { self.quitRequested = true },
            ])
        menu?.searchable = false
        return false
    }
    func requestClearCache() {
        var items = [MenuItem("Keep cache") { self.closeMenu() }]
        if manager?.active != true {
            items.append(
                MenuItem(
                    "Clear this project's DerivedData (\(Self.bytes(cacheBytes))). Keep history, logs, and results."
                ) {
                    self.closeMenu()
                    self.clearCache(.project)
                })
        }
        if let others = otherProjectsBytes {
            items.append(
                MenuItem("Clear other projects' DerivedData (\(Self.bytes(others)))") {
                    self.closeMenu()
                    self.clearCache(.others)
                })
        }
        guard items.count > 1 else {
            status = "Stop active activities before clearing DerivedData"
            return
        }
        showMenu("Clear build cache?", items)
        menu?.searchable = false
    }
    /// DerivedData of projects other than this one, when there is any.
    var otherProjectsBytes: Int64? {
        guard buildCache != nil, let total = cacheTotalBytes, total > cacheBytes else { return nil }
        return total - cacheBytes
    }
    func warnLowSpace(_ free: Int64, then start: @escaping @MainActor () -> Void) {
        var items: [MenuItem] = []
        if let others = otherProjectsBytes {
            items.append(
                MenuItem("Clear other projects' DerivedData (\(Self.bytes(others))), then continue") {
                    self.lowSpaceAcknowledged = true
                    self.closeMenu()
                    self.clearCache(.others, then: start)
                })
        }
        items.append(
            MenuItem("Continue anyway") {
                self.lowSpaceAcknowledged = true
                self.closeMenu()
                start()
            })
        items.append(MenuItem("Cancel") { self.closeMenu() })
        showMenu("Only \(Self.bytes(free)) of disk space left", items)
        menu?.searchable = false
    }
    /// Measures this project's DerivedData and, outside tests, every project's and the free disk space.
    func refreshCacheSize() {
        cacheSizeTask?.cancel()
        guard let store = manager?.store else { return }
        let all = buildCache
        let token = generation
        let scan = Task.detached(priority: .utility) {
            (
                project: store.buildCache.size(project: store.cache),
                total: all.map { $0.usage().reduce(0) { $0 + $1.bytes } },
                free: all.flatMap { BuildCache.availableCapacity(at: $0.root) }
            )
        }
        cacheSizeTask = Task {
            let sizes = await withTaskCancellationHandler {
                await scan.value
            } onCancel: {
                scan.cancel()
            }
            guard !Task.isCancelled && token == generation else { return }
            cacheBytes = sizes.project
            cacheTotalBytes = sizes.total
            freeBytes = sizes.free
        }
    }
    var cloudInformation: String {
        let product = cloudProducts.first { $0.id == cloudProduct }?.name ?? "Not selected"
        let workflow = cloudWorkflows.first { $0.id == cloudWorkflow }?.name ?? "All workflows"
        let interval = cloudPage.items.contains(where: \.active) ? 15 : 60
        return
            "XCODE CLOUD · READ ONLY\n\nProject: \(container.path)\nProduct: \(product)\nWorkflow: \(workflow)\nRefresh: every \(interval)s\nLast refresh: \(cloudLastRefresh?.formatted() ?? "Never")\n\n\(cloudStatus)\n\n\(loading ? status : "r refresh · a artifacts · Enter test results")"
    }
    func footer(width: Int) -> String {
        if menu != nil { return " [Enter] Select   [Esc] Back" }
        if simulatorDraft != nil {
            return simulatorCreation == nil
                ? " [Enter] Create  [Esc] Cancel  [Ctrl-U] Clear" : " Creating simulator...  [Esc] Cancel"
        }
        if loading { return " [Esc / x] Cancel   [:] Actions" }
        if showingDevices && filteringDevices { return " [Enter] Actions   [Esc] Clear filter" }
        // Workspace shortcuts are unavailable while Devices is open.
        let anchors =
            showingDevices
            ? []
            : [
                width >= 64
                    ? "[:] Actions   [m] \(cloudMode ? "Local" : "Cloud")   [?] Help" : "[:] Actions  [?] Help"
            ]
        let hints: [String]
        if showingDevices {
            hints = deviceShortcuts
        } else if pane == 2 {
            hints =
                detailText == nil
                ? ["[v] \((cloudMode ? cloudRaw : raw) ? "Summary" : "Raw")", "[G] Follow", "[y] Copy", "[j/k] Scroll"]
                : ["[Esc] Back", "[Enter] Actions", "[y] Copy"]
        } else if pane == 1 {
            hints =
                cloudMode
                ? ["[Enter] Results", "[a] Files", "[r] Refresh"]
                : selectedRecord?.phase.active == true
                    ? ["[x] Stop", "[r] Run", "[t] Test"] : ["[Enter] Results", "[r] Run", "[b] Build"]
        } else {
            hints =
                cloudMode
                ? ["[Enter] Choose", "[r] Refresh"] : ["[Enter] Choose", "[b] Build", "[r] Run", "[t] Test"]
        }
        var visible: [String] = []
        for hint in hints where (" " + (visible + [hint] + anchors).joined(separator: "   ")).count <= width {
            visible.append(hint)
        }
        return " " + (visible + anchors).joined(separator: "   ")
    }
}

extension BuildRecord {
    var statusLabel: String {
        if operation == .discoverTests && phase == .succeeded { return "TESTS" }
        if operation == .discoverTests && phase == .testing { return "LIST" }
        switch phase {
        case .queued: return "QUEUE"
        case .building: return "BUILD"
        case .testing: return "TEST"
        case .booting: return "BOOT"
        case .installing: return "INSTALL"
        case .launching: return "LAUNCH"
        case .running: return "RUN"
        case .succeeded: return operation == .test ? "PASS" : "OK"
        case .cancelled: return "STOP"
        default: return "FAIL"
        }
    }
    var operationLabel: String {
        switch operation {
        case .run: "Run"
        case .test: "Test"
        case .discoverTests: "Discover tests"
        default: "Build"
        }
    }
}

extension MenuItem {
    func displayTitle(width: Int) -> String {
        guard let destination else { return title }
        let nameWidth = width - 28
        guard nameWidth >= 8 else { return title }
        return OutputFormatter.truncate(destination.name, width: nameWidth).padding(
            toLength: nameWidth, withPad: " ", startingAt: 0)
            + " " + OutputFormatter.truncate(destination.os, width: 7).padding(toLength: 7, withPad: " ", startingAt: 0)
            + " " + destination.kindLabel.padding(toLength: 9, withPad: " ", startingAt: 0)
            + " " + OutputFormatter.truncate(destination.state, width: 9)
    }
}
