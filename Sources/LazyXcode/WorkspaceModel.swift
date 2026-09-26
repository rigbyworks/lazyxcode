import AppKit
import Foundation
import LazyXcodeCore
import Observation

@MainActor
struct MenuItem: Identifiable {
    let id: String
    let title: String
    let destination: Destination?
    let action: @MainActor () -> Void
    init(_ title: String, id: String? = nil, destination: Destination? = nil, action: @escaping @MainActor () -> Void) {
        self.id = id ?? title
        self.title = title
        self.action = action
        self.destination = destination
    }
}

@MainActor
struct MenuState {
    var title: String
    var items: [MenuItem]
    var query = ""
    var searchable = true
    var index = 0
    var filtered: [MenuItem] { items.filter { query.isEmpty || $0.title.localizedCaseInsensitiveContains(query) } }
}

@MainActor @Observable
final class WorkspaceModel {
    let containers: [Container]
    var container: Container
    var schemes: [String] = []
    var scheme = ""
    var destinations: [Destination] = []
    var destinationID = ""
    var manager: BuildManager?
    var preferences: Preferences
    var status = "Loading schemes and destinations..."
    var pane = 0
    var buildRow = 0
    var selectedRecordID: String?
    var raw = false
    var cloudMode = false
    var cloudRaw = false
    var outputText = ""
    var outputSummary: BuildOutput?
    var detailText: String?
    var detailActions: [MenuItem] = []
    var outputOffset = 0
    var outputWidth = 60
    var outputHeight = 20
    var follow = true
    var menu: MenuState?
    var menuParents: [MenuState] = []
    var loading = false
    var cloudProducts: [CloudResource] = []
    var cloudWorkflows: [CloudResource] = []
    var cloudProduct = ""
    var cloudWorkflow = ""
    var cloudPage = CloudPage(items: [], included: [], next: "")
    var selectedCloudID: String?
    var cloudDetails: [String: CloudDetails] = [:]
    var cloudLogs: [String: String] = [:]
    var cloudStatus = "Press r to connect"
    var cloudRefreshing = false
    var cloudLastRefresh: Date?
    var clock = Date()
    var quitRequested = false
    var cacheBytes: Int64 = 0
    var pausedOutput: String?
    var savedLocalOutput = OutputPosition()
    var savedCloudOutput = OutputPosition()
    struct OutputPosition {
        var offset = 0
        var follow = true
        var detail: String?
        var actions: [MenuItem] = []
        var paused: String?
    }
    @ObservationIgnored var cacheSizeTask: Task<Void, Never>?
    @ObservationIgnored var wrappedCache: (text: String, width: Int, lines: [String])?

    @ObservationIgnored let client: XcodeClient
    @ObservationIgnored var managers: [String: BuildManager] = [:]
    var discovery: Task<Void, Never>?
    @ObservationIgnored var discoveryID = UUID()
    var destinationRefresh: Task<Void, Never>?
    @ObservationIgnored var destinationRefreshID = UUID()
    @ObservationIgnored var pending: Task<Void, Never>?
    @ObservationIgnored var cloudRefresh: Task<Void, Never>?
    @ObservationIgnored var cloudClient: CloudClient?
    @ObservationIgnored var generation = UUID()
    @ObservationIgnored var cloudGeneration = UUID()
    @ObservationIgnored var historyLoad: Task<Void, Never>?
    @ObservationIgnored var queuedRequests: [UUID: Task<Void, Never>] = [:]
    @ObservationIgnored var shuttingDown = false
    @ObservationIgnored var pendingID = UUID()
    @ObservationIgnored var outputVersion = -1
    @ObservationIgnored var outputRecordID: String?
    @ObservationIgnored var pageStart: UInt64 = 0
    @ObservationIgnored var pageEnd: UInt64?
    @ObservationIgnored var newerPages: [UInt64?] = []
    @ObservationIgnored var lastDestinationRefresh = Date.distantPast
    @ObservationIgnored var discoveryRecordID: String?

    init(containers: [Container], preferences: Preferences = Preferences(), client: XcodeClient = XcodeClient()) {
        self.containers = containers
        self.preferences = preferences
        self.client = client
        let directory = URL(fileURLWithPath: containers[0].path).deletingLastPathComponent().path
        container = containers.first { $0.path == preferences.containers[directory] } ?? containers[0]
    }
    var records: [BuildRecord] { manager?.records ?? [] }
    var selectedRecord: BuildRecord? { records.first { $0.id == selectedRecordID } ?? records.first }
    var selectedCloud: CloudResource? { cloudPage.items.first { $0.id == selectedCloudID } ?? cloudPage.items.first }
    var destination: Destination? { destinations.first { $0.id == destinationID } }
    var coverage: Bool { preferences.coverageDisabled[container.path] != true }
    var title: String { cloudMode ? "Xcode Cloud · read-only" : "Local" }
    var discoveryCache: DiscoveryCache? { manager.map { DiscoveryCache(store: $0.store) } }
    var discoveryStatus: String? {
        if discovery != nil && destinationRefresh != nil {
            return schemes.isEmpty || destinations.isEmpty
                ? "Loading schemes and destinations..." : "Refreshing schemes and destinations..."
        }
        if discovery != nil { return schemes.isEmpty ? "Loading schemes..." : "Refreshing schemes..." }
        if destinationRefresh != nil {
            return destinations.isEmpty ? "Loading destinations..." : "Refreshing destinations..."
        }
        return nil
    }
    var menuDiscoveryStatus: String? {
        if menu?.title == "Scheme", discovery != nil { return "Loading schemes..." }
        if menu?.title == "Destination", destinationRefresh != nil { return "Loading destinations..." }
        return nil
    }

    func start() {
        let directory = URL(fileURLWithPath: container.path).deletingLastPathComponent().path
        let needsPicker = containers.count > 1 && preferences.containers[directory] == nil
        chooseContainer(container)
        if needsPicker { openContainers() }
    }
    func savePreferences() {
        do { try preferences.save() } catch { status = "Could not save preferences: \(error.localizedDescription)" }
    }
    func chooseContainer(_ value: Container) {
        cancelPending()
        discovery?.cancel()
        destinationRefresh?.cancel()
        discoveryID = UUID()
        destinationRefreshID = UUID()
        cloudRefresh?.cancel()
        generation = UUID()
        cloudGeneration = UUID()
        discovery = nil
        destinationRefresh = nil
        container = value
        schemes = []
        scheme = ""
        destinations = []
        destinationID = ""
        selectedRecordID = nil
        savedLocalOutput = OutputPosition()
        savedCloudOutput = OutputPosition()
        resetOutput()
        cloudPage = CloudPage(items: [], included: [], next: "")
        selectedCloudID = nil
        cloudProducts = []
        cloudWorkflows = []
        cloudDetails = [:]
        cloudLogs = [:]
        cloudProduct = preferences.cloudProducts[value.path] ?? ""
        cloudWorkflow = preferences.cloudWorkflows[value.path] ?? ""
        cloudLastRefresh = nil
        cloudRefreshing = false
        do {
            if let existing = managers[value.path] {
                manager = existing
            } else {
                let new = try BuildManager(store: ProjectStore(container: value), client: client)
                managers[value.path] = new
                manager = new
            }
            let directory = URL(fileURLWithPath: value.path).deletingLastPathComponent().path
            preferences.containers[directory] = value.path
            savePreferences()
            reload(useCache: true)
            refreshCacheSize()
        } catch {
            manager = nil
            status = error.localizedDescription
        }
    }
    func reload(useCache: Bool = false) {
        guard manager?.active != true else {
            status = "Stop active activities before reloading project metadata"
            return
        }
        discovery?.cancel()
        discoveryID = UUID()
        let requestID = discoveryID
        let token = generation
        let container = container
        let cache = discoveryCache
        if useCache, let cached = cache?.schemes(), !cached.isEmpty {
            schemes = cached
            let remembered = preferences.schemes[container.path] ?? ""
            chooseScheme(cached.contains(remembered) ? remembered : cached[0])
        } else if scheme.isEmpty, let remembered = preferences.schemes[container.path], !remembered.isEmpty {
            // Start destination discovery while Xcode validates the remembered scheme.
            // The fresh scheme list below replaces it if the scheme was removed.
            chooseScheme(remembered)
        } else if !scheme.isEmpty {
            // Refresh both queries concurrently when we already know a scheme.
            destinationRefresh?.cancel()
            destinationRefresh = nil
            refreshDestinations(force: true)
        }
        status = schemes.isEmpty ? "Loading schemes..." : "Refreshing schemes and destinations..."
        discovery = Task {
            defer {
                if requestID == discoveryID {
                    discovery = nil
                    updateDestinationStatus(recovering: false)
                }
            }
            do {
                let found = try await client.schemes(container)
                guard !Task.isCancelled, token == generation, requestID == discoveryID else { return }
                cache?.saveSchemes(found)
                schemes = found
                if var menu, menu.title == "Scheme" {
                    let selected = menu.filtered.indices.contains(menu.index) ? menu.filtered[menu.index].id : nil
                    menu.items = schemeItems()
                    menu.index = menu.filtered.firstIndex(where: { $0.id == selected }) ?? 0
                    self.menu = menu
                }
                let remembered = scheme.isEmpty ? preferences.schemes[container.path] ?? "" : scheme
                let selected = found.contains(remembered) ? remembered : found.first ?? ""
                if selected != scheme || selected.isEmpty { chooseScheme(selected) }
                if found.isEmpty {
                    status = "No schemes. Share a scheme in Xcode and press R."
                } else if destinationRefresh == nil {
                    updateDestinationStatus(recovering: false)
                }
            } catch { if !Task.isCancelled && token == generation { status = error.localizedDescription } }
        }
    }
    func chooseScheme(_ value: String) {
        if value == scheme && !value.isEmpty {
            refreshDestinations()
            return
        }
        destinationRefresh?.cancel()
        destinationRefreshID = UUID()
        destinationRefresh = nil
        scheme = value
        destinations = value.isEmpty ? [] : discoveryCache?.destinations(scheme: value) ?? []
        destinationID = preferences.simulators[container.path + "\0" + scheme] ?? ""
        if !destinations.isEmpty, !destinations.contains(where: { $0.id == destinationID }) {
            destinationID = destinations[0].id
        }
        preferences.schemes[container.path] = value
        savePreferences()
        lastDestinationRefresh = .distantPast
        if !value.isEmpty {
            status = destinations.isEmpty ? "Loading destinations..." : "Refreshing destinations..."
            refreshDestinations(force: true)
        }
    }
    func refreshDestinations(force: Bool = false) {
        guard destinationRefresh == nil, !scheme.isEmpty else { return }
        guard force || Date().timeIntervalSince(lastDestinationRefresh) >= 15 else { return }
        destinationRefreshID = UUID()
        let requestID = destinationRefreshID
        let token = generation
        let scheme = scheme
        let container = container
        let cache = discoveryCache
        lastDestinationRefresh = Date()
        destinationRefresh = Task {
            defer {
                if requestID == destinationRefreshID {
                    lastDestinationRefresh = Date()
                    destinationRefresh = nil
                    updateDestinationStatus(recovering: false)
                }
            }
            do {
                let found = try await client.destinations(container, scheme: scheme)
                guard !Task.isCancelled, token == generation, requestID == destinationRefreshID else { return }
                cache?.saveDestinations(found, scheme: scheme)
                destinations = found
                if !found.contains(where: { $0.id == destinationID }) { destinationID = found.first?.id ?? "" }
                if var menu, menu.title == "Destination" {
                    let selected = menu.filtered.indices.contains(menu.index) ? menu.filtered[menu.index].id : nil
                    menu.items = destinationItems()
                    menu.index = menu.filtered.firstIndex(where: { $0.id == selected }) ?? 0
                    self.menu = menu
                }
                updateDestinationStatus()
            } catch {
                if !Task.isCancelled && token == generation {
                    status = "Destination refresh failed: \(error.localizedDescription)"
                }
            }
        }
        updateDestinationStatus(recovering: false)
    }
    func updateDestinationStatus(recovering: Bool = true) {
        if status == "Ready" || status.hasPrefix("Loading") || status.hasPrefix("Refreshing")
            || status.hasPrefix("No compatible") || recovering && status.hasPrefix("Destination refresh failed")
        {
            status =
                discoveryStatus
                ?? (destinations.isEmpty
                    ? "No compatible destinations. Install a simulator runtime or connect a device." : "Ready")
        }
    }
    func chooseDestination(_ id: String) {
        destinationID = id
        preferences.simulators[container.path + "\0" + scheme] = id
        savePreferences()
    }
    func queue(
        _ operation: LazyXcodeCore.Operation, targets: [String] = [], scope: String = "All tests",
        original: BuildRecord? = nil
    ) {
        guard !shuttingDown, !cloudMode, let manager else { return }
        guard discovery == nil || schemes.contains(scheme) && destination != nil else {
            status = "Wait for project discovery to finish"
            return
        }
        guard let destination = original?.simulator ?? destination, !(original?.scheme ?? scheme).isEmpty else {
            status = "Choose a scheme and destination first"
            return
        }
        let container = original?.container ?? container
        let scheme = original?.scheme ?? scheme
        let coverage = original?.coverage ?? coverage
        let token = generation
        let requestID = UUID()
        queuedRequests[requestID] = Task {
            defer { queuedRequests[requestID] = nil }
            do {
                let id = try await manager.start(
                    container: container, scheme: scheme, destination: destination,
                    operation: operation, targets: targets, scope: scope, coverage: coverage)
                guard token == generation else { return }
                selectedRecordID = id
                resetOutput()
                pane = 2
                if operation == .discoverTests { discoveryRecordID = id }
                status = "Started \(operation.rawValue)"
            } catch { if !Task.isCancelled && token == generation { status = error.localizedDescription } }
        }
    }
    func tick() {
        guard !shuttingDown else { return }
        let now = Date()
        if Int(now.timeIntervalSince1970) != Int(clock.timeIntervalSince1970) { clock = now }
        if !cloudMode && discovery == nil && clock.timeIntervalSince(lastDestinationRefresh) >= 15 {
            refreshDestinations()
        }
        if cloudMode {
            let interval: TimeInterval = cloudPage.items.contains(where: \.active) ? 15 : 60
            if clock.timeIntervalSince(cloudLastRefresh ?? .distantPast) >= interval && !cloudRefreshing {
                refreshCloud()
            }
            return
        }
        if let manager, !manager.lastError.isEmpty { status = manager.lastError }
        if let id = discoveryRecordID, let record = records.first(where: { $0.id == id }), !record.phase.active {
            discoveryRecordID = nil
            if record.phase == .succeeded && selectedRecord?.id == id { openDiscoveredTests(record) }
        }
        guard detailText == nil, pageEnd == nil, let record = selectedRecord else { return }
        if outputRecordID != record.id {
            outputRecordID = record.id
            outputVersion = -1
        }
        if let log = manager?.log(record.id) {
            if log.version != outputVersion {
                let snapshot = log.snapshot()
                outputVersion = snapshot.version
                outputText = snapshot.text
                outputSummary = log.summary()
            }
        } else if outputVersion != Int.max {
            do { outputText = try manager?.store.logPage(record, size: 256 * 1024).text ?? "" } catch {
                outputText = "No retained log: \(error.localizedDescription)"
            }
            outputVersion = Int.max
            loadHistorySummary(record)
        }
    }
    func resetOutput() {
        pausedOutput = nil
        historyLoad?.cancel()
        historyLoad = nil
        outputSummary = nil
        detailText = nil
        detailActions = []
        outputText = ""
        outputOffset = 0
        follow = true
        outputVersion = -1
        outputRecordID = nil
        pageEnd = nil
        newerPages = []
    }
    func loadHistorySummary(_ record: BuildRecord) {
        historyLoad?.cancel()
        guard let store = manager?.store else { return }
        let url = URL(fileURLWithPath: record.logPath)
        guard Store.contains(url, in: store.state.appendingPathComponent("logs")) else { return }
        let reader = Task.detached {
            let file = try FileHandle(forReadingFrom: url)
            defer { try? file.close() }
            var summary = BuildOutput()
            while let chunk = try file.read(upToCount: 64 * 1024), !chunk.isEmpty {
                try Task.checkCancellation()
                summary.append(chunk)
            }
            summary.finish(at: record.finishedAt ?? Date())
            return summary
        }
        historyLoad = Task {
            do {
                let summary = try await withTaskCancellationHandler {
                    try await reader.value
                } onCancel: {
                    reader.cancel()
                }
                guard !Task.isCancelled, selectedRecord?.id == record.id else { return }
                outputSummary = summary
            } catch { if !Task.isCancelled { status = "Could not read complete log: \(error.localizedDescription)" } }
        }
    }
    func selectRecord(_ id: String) {
        cancelPending()
        selectedRecordID = id
        resetOutput()
        tick()
    }
    func move(_ amount: Int) {
        if pane == 0 {
            let count = cloudMode ? 3 : 2
            buildRow = ((buildRow + amount) % count + count) % count
        } else if pane == 1 {
            if cloudMode {
                let items = cloudPage.items
                let index = items.firstIndex { $0.id == selectedCloud?.id } ?? 0
                if index + amount >= items.count && !cloudPage.next.isEmpty {
                    refreshCloud(older: true)
                } else if !items.isEmpty {
                    selectCloud(items[min(items.count - 1, max(0, index + amount))].id)
                }
            } else {
                let index = records.firstIndex { $0.id == selectedRecord?.id } ?? 0
                if !records.isEmpty { selectRecord(records[min(records.count - 1, max(0, index + amount))].id) }
            }
        } else {
            if follow { pausedOutput = displayedText }
            let last = max(0, outputLines(width: outputWidth).count - outputHeight)
            let current = follow ? last : min(last, outputOffset)
            follow = false
            outputOffset = min(last, max(0, current + amount))
        }
    }
    func jump(last: Bool) {
        if pane == 1 {
            if cloudMode, let run = last ? cloudPage.items.last : cloudPage.items.first {
                selectCloud(run.id)
            } else if let record = last ? records.last : records.first {
                selectRecord(record.id)
            }
        } else if pane == 2 {
            if !last && follow { pausedOutput = displayedText }
            if last { pausedOutput = nil }
            follow = last
            outputOffset = 0
            if last && !cloudMode && detailText == nil {
                pageEnd = nil
                newerPages = []
                outputVersion = -1
            }
        }
    }
    func logPage(older: Bool) {
        pausedOutput = nil
        guard !cloudMode, let record = selectedRecord, let store = manager?.store else { return }
        do {
            let end: UInt64?
            if older {
                if let pageEnd {
                    guard pageStart > 0 else {
                        status = "Beginning of log"
                        return
                    }
                    newerPages.append(pageEnd)
                    end = pageStart
                } else {
                    end = nil
                }
            } else {
                guard !newerPages.isEmpty else {
                    pageEnd = nil
                    follow = true
                    outputVersion = -1
                    tick()
                    status = "Following latest output"
                    return
                }
                end = newerPages.removeLast()
            }
            let page = try store.logPage(record, end: end)
            pageStart = page.start
            pageEnd = page.end
            outputText = page.text
            raw = true
            detailText = nil
            follow = false
            outputOffset = 0
            if end == nil { outputVersion = -1 }
            status = "Log bytes \(page.start)...\(page.end)"
        } catch { status = error.localizedDescription }
    }
    func outputLines(width: Int) -> [String] {
        let text = displayedText
        if let cached = wrappedCache, cached.width == width, cached.text == text { return cached.lines }
        let lines = OutputFormatter.wrappedLines(text, width: max(1, width), limit: nil)
        wrappedCache = (text, width, lines)
        return lines
    }
    var displayedText: String {
        if let pausedOutput, !follow { return pausedOutput }
        if let detailText { return detailText }
        if cloudMode {
            guard let run = selectedCloud else { return cloudStatus }
            return cloudRaw
                ? cloudLogs[run.id] ?? "Press v to load log artifacts."
                : cloudStatus + "\n\n" + (cloudDetails[run.id]?.text ?? "Loading build details...")
        }
        guard let record = selectedRecord else {
            return
                "Ready when you are\n\n[b] Build  [r] Run  [t] Test\n[1] Choose a scheme and destination\n\n[2] Browse activity history\n[3] Read output and results\n\nPress ? for keyboard help."
        }
        if raw { return pageEnd == nil ? OutputFormatter.rawWindow(outputText) : outputText }
        let header = "\(record.scheme) · \(record.simulator.label)\n\(record.statusLabel) · \(record.duration)\n\n"
        var text = header + (outputSummary?.text(now: clock, phase: record.phase) ?? "Loading build summary...")
        if let error = record.error, !error.isEmpty { text += "\n\n[\(record.statusLabel)] \(error)" }
        if !record.phase.active && record.resultBundlePath != nil {
            text += "\n\n[Enter] Results, rerun failures, coverage"
        }
        if record.operation == .discoverTests && record.phase == .succeeded {
            text += "\n\n[Enter] Choose a test to run"
        }
        return text
    }
    func showDetail(_ text: String, actions: [MenuItem] = []) {
        pausedOutput = nil
        detailText = text
        detailActions = actions
        outputOffset = 0
        follow = false
        pane = 2
    }
    func showMenu(_ title: String, _ items: [MenuItem], nested: Bool = false, selectedID: String? = nil) {
        cancelPending()
        if nested, let menu { menuParents.append(menu) } else { menuParents = [] }
        var next = MenuState(title: title, items: items)
        next.index = items.firstIndex { $0.id == selectedID } ?? 0
        menu = next
    }
    func closeMenu() {
        menu = nil
        menuParents = []
    }
    func back() {
        if loading {
            cancelPending()
            return
        }
        if menu != nil {
            menu = menuParents.popLast()
            return
        }
        guard detailText != nil else { return }
        pausedOutput = nil
        detailText = nil
        detailActions = []
        outputOffset = 0
        follow = true
    }
    func activateMenu() {
        guard let menu, menu.filtered.indices.contains(menu.index) else { return }
        let action = menu.filtered[menu.index].action
        action()
    }
    func openContainers() {
        showMenu(
            "Project or workspace",
            containers.map { value in
                MenuItem(value.name) {
                    self.closeMenu()
                    self.chooseContainer(value)
                }
            })
    }
    func destinationItems() -> [MenuItem] {
        destinations.map { value in
            MenuItem("\(value.label) · \(value.kindLabel) · \(value.state)", id: value.id, destination: value) {
                self.closeMenu()
                self.chooseDestination(value.id)
            }
        }
    }
    func schemeItems() -> [MenuItem] {
        schemes.map { value in
            MenuItem(value, id: value) {
                self.closeMenu()
                self.chooseScheme(value)
            }
        }
    }
    func activate() {
        if pane == 2, detailText != nil && !detailActions.isEmpty {
            showMenu("Result actions", detailActions)
        } else if pane == 0 {
            openBuildSetting(buildRow)
        } else if cloudMode {
            openArtifacts(resultsOnly: true)
        } else if let record = selectedRecord {
            openTestActivity(record)
        }
    }
    func openBuildSetting(_ row: Int) {
        pane = 0
        buildRow = row
        if cloudMode {
            openCloudSetting()
            return
        }
        switch row {
        case 0:
            showMenu("Scheme", schemeItems(), selectedID: scheme)
        case 1:
            refreshDestinations()
            showMenu("Destination", destinationItems(), selectedID: destinationID)
        default: toggleCoverage()
        }
    }
    func toggleCoverage() {
        preferences.coverageDisabled[container.path] = coverage
        savePreferences()
        status = "Coverage \(coverage ? "on" : "off")"
    }
    func cancelSelected() {
        if loading { cancelPending() } else if !cloudMode, let record = selectedRecord { manager?.cancel(record.id) }
    }
    func cancelPending() {
        pending?.cancel()
        pending = nil
        pendingID = UUID()
        loading = false
    }
    func load(_ title: String, work: @escaping @MainActor () async throws -> Void) {
        cancelPending()
        let token = pendingID
        loading = true
        status = title
        pending = Task {
            defer {
                if token == pendingID {
                    loading = false
                    pending = nil
                }
            }
            do { try await work() } catch {
                if !Task.isCancelled && token == pendingID { status = error.localizedDescription }
            }
        }
    }
    func openProject() {
        load("Opening Xcode...") { try await self.client.runner.run("open", ["-a", "Xcode", self.container.path]) }
    }
    func clearCache() {
        do {
            try manager?.clearCache()
            status = "Cleared managed DerivedData"
        } catch { status = error.localizedDescription }
    }
    func copyOutput() {
        let text = OutputFormatter.sanitize(displayedText)
        NSPasteboard.general.clearContents()
        status = NSPasteboard.general.setString(text, forType: .string) ? "Copied output" : "Copy failed"
    }
    func information() {
        if cloudMode {
            showDetail(cloudInformation)
            return
        }
        showDetail(
            "\(container.path)\n\n\(cloudMode ? cloudStatus : status)\n\nScheme: \(scheme)\nDestination: \(destination?.label ?? "None")\n\nLog: \(selectedRecord?.logPath ?? "None")\nResults: \(selectedRecord?.resultBundlePath ?? "None")"
        )
    }
    func shutdown() async {
        shuttingDown = true
        let background =
            [historyLoad, pending, discovery, destinationRefresh, cloudRefresh, cacheSizeTask].compactMap { $0 }
            + Array(queuedRequests.values)
        for task in background { task.cancel() }
        cancelPending()
        for task in background { await task.value }
        for manager in managers.values { await manager.shutdown() }
    }
}

enum OutputFormatter {
    static func sanitize(_ text: String) -> String {
        let ansi = text.replacingOccurrences(of: "\u{1B}\\[[0-?]*[ -/]*[@-~]", with: "", options: .regularExpression)
            .replacingOccurrences(
                of: "\u{1B}\\][^\u{7}\u{1B}]*(?:\u{7}|\u{1B}\\\\)", with: "", options: .regularExpression)
        return String(
            ansi.unicodeScalars.filter { $0.value == 10 || $0.value == 9 || $0.value >= 32 && $0.value != 127 })
    }
    static func rawWindow(_ text: String) -> String {
        let lines = sanitize(text).components(separatedBy: .newlines)
        var shortened = lines.count > 2000 || text.utf8.count >= 256 * 1024
        let visible = lines.suffix(2000).map { line in
            if line.utf8.count > 4096 {
                shortened = true
                return "[long line truncated] " + String(line.suffix(4096))
            }
            return line
        }.joined(separator: "\n")
        return (shortened ? "[Earlier output omitted from this window; [ / ] browse the complete log.]\n" : "")
            + visible
    }
    static func truncate(_ text: String, width: Int) -> String {
        guard text.count > max(0, width) else { return text }
        return String(text.prefix(max(0, width - 1))) + (width > 0 ? "…" : "")
    }
    static func lines(_ text: String, limit: Int? = 2000) -> [String] {
        let lines = sanitize(text).components(separatedBy: .newlines)
        guard let limit else { return lines }
        return lines.suffix(limit).map { String($0.prefix(4096)) }
    }
}
