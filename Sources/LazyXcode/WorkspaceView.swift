import Foundation
import LazyXcodeCore
import SwiftTUI

struct WorkspaceView: View {
    let model: WorkspaceModel
    var live = true

    var body: some View {
        GeometryReader { geometry in
            let width = geometry.size.width
            let height = geometry.size.height
            let layout = WorkspaceLayout(width: width, height: height, pane: model.pane)
            VStack(alignment: .leading, spacing: 0) {
                HeaderView(model: model, width: width)
                ZStack {
                    if width < 44 || height < 10 {
                        Text("Terminal is too small. Current: \(width)×\(height). Required: 44×10.")
                            .frame(maxWidth: .infinity, maxHeight: .infinity)
                    } else {
                        WorkspacePanes(model: model, width: width, height: layout.contentHeight)
                    }
                    if let menu = model.menu {
                        MenuView(menu: menu, width: min(76, width - 2), height: min(16, height - 2))
                            .background(.background)
                    }
                }.frame(width: width, height: layout.contentHeight)
                Text(model.footer(width: width)).foregroundStyle(.info).lineLimit(1)
                    .frame(width: width, alignment: .leading)
            }
            .focusable()
            .onKeyPress { key in
                model.outputWidth = layout.outputWidth
                model.outputHeight = layout.outputHeight
                return handle(key)
            }
        }
        .task {
            guard live else { return }
            model.start()
            while !Task.isCancelled {
                model.tick()
                do { try await Task.sleep(for: .milliseconds(50)) } catch { break }
            }
        }
    }

    func handle(_ press: KeyPress) -> KeyPressResult {
        if press.modifiers.contains(.ctrl) {
            if press.key == .character("c") { return model.requestQuit() ? .ignored : .handled }
            return .ignored
        }
        if model.loading && (press.key == .escape || press.key == .character("x")) {
            model.cancelPending()
            return .handled
        }
        if model.menu != nil {
            switch press.key {
            case .escape: model.back()
            case .arrowUp: model.menu!.index = max(0, model.menu!.index - 1)
            case .arrowDown: model.menu!.index = min(max(0, model.menu!.filtered.count - 1), model.menu!.index + 1)
            case .pageUp: model.menu!.index = max(0, model.menu!.index - 10)
            case .pageDown: model.menu!.index = min(max(0, model.menu!.filtered.count - 1), model.menu!.index + 10)
            case .return:
                if !model.loading { model.activateMenu() }
                if model.quitRequested { return .ignored }
            case .backspace:
                if !model.menu!.query.isEmpty {
                    model.menu!.query.removeLast()
                    model.menu!.index = 0
                }
            case .character(let character):
                if model.menu?.searchable == false {
                    if character == "q" { model.back() }
                    return .handled
                }
                model.menu!.query.append(character)
                model.menu!.index = 0
            case .space:
                model.menu!.query.append(" ")
                model.menu!.index = 0
            default: return .ignored
            }
            return .handled
        }
        switch press.key {
        case .tab: model.pane = (model.pane + (press.modifiers.contains(.shift) ? 2 : 1)) % 3
        case .character("1"): model.pane = 0
        case .character("2"): model.pane = 1
        case .character("3"): model.pane = 2
        case .arrowUp, .character("k"): model.move(-1)
        case .arrowDown, .character("j"): model.move(1)
        case .pageUp: if model.pane == 2 { model.move(-10) }
        case .pageDown: if model.pane == 2 { model.move(10) }
        case .home, .character("g"): model.jump(last: false)
        case .end, .character("G"): model.jump(last: true)
        case .return: model.activate()
        case .escape: model.back()
        case .character("b"): model.queue(.build)
        case .character("r"): if model.cloudMode { model.refreshCloud() } else { model.queue(.run) }
        case .character("t"): model.testMenu()
        case .character("x"): model.cancelSelected()
        case .character("c"): if !model.cloudMode { model.requestClearCache() }
        case .character("R"): if !model.cloudMode { model.reload() }
        case .character("L"): if model.cloudMode && !model.cloudPage.next.isEmpty { model.refreshCloud(older: true) }
        case .character("a"): if model.cloudMode { model.openArtifacts() }
        case .character("o"): model.openProject()
        case .character("m"): model.toggleMode()
        case .character("v"): model.toggleOutput()
        case .character("y"): model.copyOutput()
        case .character("["): model.logPage(older: true)
        case .character("]"): model.logPage(older: false)
        case .character("i"): model.information()
        case .character("?"): model.showDetail(Self.help)
        case .character("q"):
            if model.detailText == Self.help {
                model.back()
                return .handled
            }
            return model.requestQuit() ? .ignored : .handled
        case .character(":"): model.actionMenu()
        default: return .ignored
        }
        return .handled
    }

    static let help = """
        Navigation
          1 / 2 / 3       Build / Activity / Output
          Tab / Shift-Tab Cycle panes
          j / k / arrows  Select or scroll
          g / G           First / last, top / follow output
          Enter           Choose a value or inspect results
          Esc             Back, cancel result loading
          :               Search actions
          i               Full status and paths
          q / Ctrl-C      Quit and cancel active commands

        Local
          b               Build
          r               Build and run
          t               All / unit / UI / individual tests and coverage
          x               Cancel the selected activity
          c               Clear managed DerivedData
          R               Reload schemes and destinations
          [ / ]           Older / newer raw log page

        Cloud, read-only
          m               Switch Local / Cloud
          r               Refresh runs
          L               Load older runs
          a               Download artifacts
          Enter           Inspect a test-result artifact
          x               Cancel a download or result load

        Output
          v               Concise / raw output
          y               Copy displayed output
          o               Open the project in Xcode

        Pickers
          Type            Filter the list
          Up / Down       Select a row
          Enter           Choose
          Esc             Back
        """
}

private struct HeaderView: View {
    let model: WorkspaceModel
    let width: Int
    var body: some View {
        let title = " lazyxcode | \(model.container.name)" + (model.cloudMode ? " | Xcode Cloud" : "")
        let status = OutputFormatter.sanitize(model.cloudMode ? model.cloudStatus : model.status)
            .replacingOccurrences(of: "\n", with: " · ")
        HStack(spacing: 1) {
            if width >= 74 {
                Text(OutputFormatter.truncate(title, width: width / 2)).bold()
                Spacer(minLength: 1)
                Text(OutputFormatter.truncate(status, width: width / 2 - 6) + " [i]").foregroundStyle(.muted)
            } else {
                Text(OutputFormatter.truncate(status.isEmpty ? title : status, width: width - 5) + " [i]")
            }
        }.frame(width: width, height: 1, alignment: .leading)
    }
}

private struct WorkspacePanes: View {
    let model: WorkspaceModel
    let width: Int
    let height: Int
    var body: some View {
        let layout = WorkspaceLayout(width: width, height: height + 2, pane: model.pane)
        HStack(alignment: .top, spacing: 0) {
            VStack(spacing: 0) {
                BuildPane(model: model, width: layout.sidebar, height: layout.buildHeight)
                ActivityPane(model: model, width: layout.sidebar, height: height - layout.buildHeight)
            }.frame(width: layout.sidebar, height: height)
            OutputPane(model: model, width: width - layout.sidebar, height: height)
        }.frame(width: width, height: height)
    }
}

private struct Pane<Content: View>: View {
    let title: String
    let focused: Bool
    let width: Int
    let height: Int
    @ViewBuilder var content: Content
    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            if height > 2 { content }
            Spacer(minLength: 0)
        }
        .frame(width: max(0, width - 2), height: max(0, height - 2), alignment: .topLeading)
        .padding(1)
        .frame(width: width, height: max(2, height), alignment: .topLeading)
        .border(focused ? SemanticShapeStyle.info : SemanticShapeStyle.muted)
        .overlay(alignment: .topLeading) {
            Text(" " + OutputFormatter.truncate(title, width: width - 5) + " ")
                .bold().foregroundStyle(focused ? SemanticShapeStyle.info : SemanticShapeStyle.muted).background(
                    .background
                )
                .padding(.leading, 2)
        }
    }
}

private struct BuildPane: View {
    let model: WorkspaceModel
    let width: Int
    let height: Int
    var body: some View {
        Pane(
            title: model.cloudMode ? "Local  [Cloud] [1]" : "[Local]  Cloud [1]", focused: model.pane == 0,
            width: width,
            height: height
        ) {
            if model.cloudMode {
                setting(
                    "Product", value: model.cloudProducts.first { $0.id == model.cloudProduct }?.name ?? "Choose...",
                    row: 0)
                setting(
                    "Workflow",
                    value: model.cloudWorkflows.first { $0.id == model.cloudWorkflow }?.name ?? "All workflows", row: 1)
                setting("Connection", value: model.cloudRefreshing ? "Refreshing..." : "[i] Details", row: 2)
                if height >= 8 {
                    Text("  Read only · r refresh").foregroundStyle(.muted).lineLimit(1)
                    Text("  Last: \(model.cloudLastRefresh?.formatted(date: .omitted, time: .standard) ?? "Never")")
                        .foregroundStyle(.muted).lineLimit(1)
                }
            } else {
                if height >= 7 { Text("  Container  \(model.container.name)").lineLimit(1) }
                setting("Scheme", value: model.scheme.isEmpty ? "Loading..." : model.scheme, row: 0)
                setting("Target", value: model.destination?.label ?? "No compatible targets", row: 1)
                if height >= 8 { Text("") }
                if height >= 7 {
                    Text("  [b] Build  [r] Run  [t] Test").lineLimit(1)
                    Text(
                        "  Cache: \(ByteCountFormatter.string(fromByteCount: model.cacheBytes, countStyle: .file))  [c] Clear"
                    )
                    .foregroundStyle(.muted).lineLimit(1)
                }
            }
        }
    }
    private func setting(_ label: String, value: String, row: Int) -> some View {
        Text(
            (model.pane == 0 && model.buildRow == row ? "> " : "  ")
                + label.padding(toLength: 9, withPad: " ", startingAt: 0) + value + " [>]"
        )
        .foregroundStyle(
            model.pane == 0 && model.buildRow == row ? SemanticShapeStyle.info : SemanticShapeStyle.foreground
        ).lineLimit(1)
    }
}

private struct ActivityPane: View {
    let model: WorkspaceModel
    let width: Int
    let height: Int
    var rows: [(id: String, text: String)] {
        if model.cloudMode {
            return model.cloudPage.items.map { run in
                let workflow = model.cloudPage.related(run, "workflow")?.name ?? ""
                return (
                    run.id,
                    "\(run.statusLabel) #\(Int(run.attributes["number"].number)) \(workflow) · \(BuildOutput.duration(run.duration(now: model.clock)))"
                )
            }
        }
        return model.records.map {
            (
                $0.id,
                $0.activityRow(width: width - 4)
            )
        }
    }
    var body: some View {
        let rows = rows
        let selected = model.cloudMode ? model.selectedCloud?.id : model.selectedRecord?.id
        let index = rows.firstIndex { $0.id == selected } ?? 0
        let count = max(0, height - 2)
        let start = max(0, min(index - count / 2, rows.count - count))
        Pane(
            title: model.cloudMode ? "Cloud Activity [2]" : "Local Activity [2]", focused: model.pane == 1,
            width: width, height: height
        ) {
            if rows.isEmpty && height > 3 { Text("No activities").foregroundStyle(.muted) }
            ForEach(Array(rows.dropFirst(start).prefix(count)), id: \.id) { row in
                Text((row.id == selected ? "› " : "  ") + row.text)
                    .frame(width: max(1, width - 2), alignment: .leading)
                    .background(row.id == selected ? SemanticShapeStyle.selection : SemanticShapeStyle.background)
                    .foregroundStyle(.foreground).lineLimit(1)
            }
        }
    }
}

private struct OutputPane: View {
    let model: WorkspaceModel
    let width: Int
    let height: Int
    var body: some View {
        let lines = model.outputLines(width: max(1, width - 2))
        let count = max(1, height - 2)
        let end = max(0, lines.count - count)
        let offset = model.follow ? end : min(end, model.outputOffset)
        let active = model.cloudMode ? model.selectedCloud?.active == true : model.selectedRecord?.phase.active == true
        let label =
            model.detailText != nil
            ? "Details [Esc] Log"
            : model.pageEnd != nil && !model.cloudMode
                ? "RAW PAGE [ / ] [G] Live"
                : (model.cloudMode ? model.cloudRaw : model.raw) ? "RAW" : "CONCISE"
        Pane(
            title: "Output [3] - " + label + (model.follow && active ? " - FOLLOW" : ""),
            focused: model.pane == 2, width: width,
            height: height
        ) {
            ForEach(Array(lines.enumerated().dropFirst(offset).prefix(count)), id: \.offset) { _, line in
                Text(line.isEmpty ? " " : line).foregroundStyle(outputStyle(line)).lineLimit(1)
                    .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
    }
}

private func outputStyle(_ line: String) -> SemanticShapeStyle {
    if line.contains("error:") || line.contains("✗") || line.contains("FAILED") || line.hasPrefix("[FAIL]") {
        return .danger
    }
    if line.contains("warning:") { return .warning }
    if line.contains("✓") || line.contains("SUCCEEDED") { return .success }
    if line.hasPrefix("BUILD ") || line.hasPrefix("DIAGNOSTICS") || line.hasPrefix("TEST SUITES")
        || line == "APP CONSOLE" || line == "DEPLOYMENT" || line.contains("●")
    {
        return .info
    }
    return .foreground
}

private struct MenuView: View {
    let menu: MenuState
    let width: Int
    let height: Int
    var body: some View {
        let items = menu.filtered
        let count = max(1, height - 4)
        let start = max(0, min(menu.index - count / 2, items.count - count))
        Pane(title: menu.title, focused: true, width: width, height: height) {
            if menu.searchable { Text("Search: \(menu.query)▏").foregroundStyle(.info).lineLimit(1) }
            Text("↑ ↓ select · Enter choose · Esc back").foregroundStyle(.muted)
            if items.isEmpty { Text("No matches") }
            ForEach(Array(items.enumerated().dropFirst(start).prefix(count)), id: \.element.id) { index, item in
                Text((menu.index == index ? "› " : "  ") + item.displayTitle(width: width - 4))
                    .foregroundStyle(menu.index == index ? SemanticShapeStyle.info : SemanticShapeStyle.foreground)
                    .lineLimit(1)
            }
        }
    }
}

extension OutputFormatter {
    static func cellWidth(_ character: Character) -> Int {
        let scalars = character.unicodeScalars
        if scalars.contains(where: { $0.properties.isEmojiPresentation || $0.value == 0xFE0F }) { return 2 }
        if scalars.contains(where: { scalar in
            [
                0x1100...0x115F, 0x2E80...0xA4CF, 0xAC00...0xD7A3, 0xF900...0xFAFF,
                0xFE10...0xFE6F, 0xFF00...0xFF60, 0xFFE0...0xFFE6, 0x20000...0x3FFFD,
            ].contains { $0.contains(Int(scalar.value)) }
        }) {
            return 2
        }
        return 1
    }
    static func wrappedLines(_ text: String, width: Int, limit: Int? = 2000) -> [String] {
        lines(text, limit: limit).flatMap { line -> [String] in
            guard !line.isEmpty else { return [""] }
            var current = ""
            var cells = 0
            var result: [String] = []
            for character in line.replacingOccurrences(of: "\t", with: "    ") {
                let next = cellWidth(character)
                if cells + next > max(1, width) && !current.isEmpty {
                    result.append(current)
                    current = ""
                    cells = 0
                }
                current.append(character)
                cells += next
            }
            if !current.isEmpty { result.append(current) }
            return result
        }
    }
}

extension WorkspaceModel {
    func actionMenu() {
        var items: [MenuItem] = [
            MenuItem("Open project in Xcode") {
                self.closeMenu()
                self.openProject()
            },
            MenuItem("Switch to \(cloudMode ? "Local" : "Cloud")") { self.toggleMode() },
            MenuItem("Toggle concise / raw output") {
                self.closeMenu()
                self.toggleOutput()
            },
            MenuItem("Copy displayed output") {
                self.closeMenu()
                self.copyOutput()
            },
            MenuItem("Show full status and paths") {
                self.closeMenu()
                self.information()
            },
            MenuItem("Keyboard help") {
                self.closeMenu()
                self.showDetail(WorkspaceView.help)
            },
        ]
        if cloudMode {
            items += [
                MenuItem("Refresh Cloud builds") {
                    self.closeMenu()
                    self.refreshCloud()
                },
                MenuItem("Choose product") { self.openCloudProducts() },
                MenuItem("Choose workflow") {
                    self.buildRow = 1
                    self.openCloudSetting()
                },
                MenuItem("Download artifact") { self.openArtifacts() },
                MenuItem("Inspect test results") { self.openArtifacts(resultsOnly: true) },
            ]
        } else {
            items += [
                MenuItem("Build") {
                    self.closeMenu()
                    self.queue(.build)
                },
                MenuItem("Build and run") {
                    self.closeMenu()
                    self.queue(.run)
                },
                MenuItem("Run tests...") { self.testMenu() },
                MenuItem("Inspect test activity") {
                    if let record = self.selectedRecord { self.openTestActivity(record) }
                },
                MenuItem("Cancel selected activity") {
                    self.closeMenu()
                    self.cancelSelected()
                },
                MenuItem("Clear managed DerivedData") {
                    self.closeMenu()
                    self.requestClearCache()
                },
                MenuItem("Reload schemes and destinations") {
                    self.closeMenu()
                    self.reload()
                },
                MenuItem("Choose scheme") {
                    self.buildRow = 0
                    self.activate()
                },
                MenuItem("Choose target") {
                    self.buildRow = 1
                    self.activate()
                },
                MenuItem("Choose project or workspace") { self.openContainers() },
            ]
        }
        showMenu("Actions", items)
    }
}

struct TerminalApplication: SwiftTUIRuntime.App {
    var model: WorkspaceModel?
    nonisolated init() { model = nil }
    init(model: WorkspaceModel) { self.model = model }
    var body: some Scene {
        WindowGroup("lazyxcode") {
            if let model { WorkspaceView(model: model) }
        }.exitOnKeys([KeyPress(.character("q")), KeyPress(.character("c"), modifiers: .ctrl), KeyPress(.return)])
    }
}
