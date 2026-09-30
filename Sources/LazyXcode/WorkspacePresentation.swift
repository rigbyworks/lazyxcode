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
            cacheDetail =
                "Cache \(ByteCountFormatter.string(fromByteCount: model.cacheBytes, countStyle: .file)) · [c] Clear"
        }
    }
}

extension WorkspaceModel {
    var hasActiveActivities: Bool {
        managers.values.contains(where: \.active) || manager?.active == true || !queuedRequests.isEmpty
    }
    func requestQuit() -> Bool {
        guard simulatorCreation == nil else {
            deviceStatus = "Finishing simulator creation. Please wait before quitting."
            return false
        }
        guard hasActiveActivities else { return true }
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
        guard manager?.active != true else {
            status = "Stop active activities before clearing DerivedData"
            return
        }
        showMenu(
            "Clear build cache?",
            [
                MenuItem("Keep cache") { self.closeMenu() },
                MenuItem("Clear DerivedData. Keep history, logs, and results.") {
                    self.closeMenu()
                    self.clearCache()
                    self.refreshCacheSize()
                },
            ])
        menu?.searchable = false
    }
    func refreshCacheSize() {
        cacheSizeTask?.cancel()
        guard let root = manager?.store.cache.appendingPathComponent("derived-data") else { return }
        let token = generation
        let scan = Task.detached {
            var size: Int64 = 0
            let files = FileManager.default.enumerator(
                at: root, includingPropertiesForKeys: [.fileSizeKey, .isRegularFileKey])
            while let url = files?.nextObject() as? URL {
                if Task.isCancelled { break }
                if let values = try? url.resourceValues(forKeys: [.fileSizeKey, .isRegularFileKey]),
                    values.isRegularFile == true
                {
                    size += Int64(values.fileSize ?? 0)
                }
            }
            return size
        }
        cacheSizeTask = Task {
            let size = await withTaskCancellationHandler {
                await scan.value
            } onCancel: {
                scan.cancel()
            }
            if !Task.isCancelled && token == generation { cacheBytes = size }
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
        if simulatorDraft != nil {
            return simulatorCreation == nil ? " [Enter] Create  [Esc] Cancel  [Ctrl-U] Clear" : " Creating simulator..."
        }
        if menu != nil { return " [Enter] Select   [Esc] Back" }
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
            let close = deviceQuery.isEmpty ? "[Esc] Close" : "[Esc] Clear filter"
            hints =
                cloudMode
                ? ["[/] Filter", "[Enter] Details", "[R] Refresh", close]
                : ["[/] Filter", "[n] New", "[Enter] Actions", "[R] Refresh", close]
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
