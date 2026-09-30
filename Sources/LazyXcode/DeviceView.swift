import LazyXcodeCore
import SwiftTUI

struct DevicesState {
    let devices: [Destination]
    let selectedID: String?
    let refreshing: Bool
    let cloudMode: Bool
    let query: String
    let filtering: Bool

    @MainActor init(model: WorkspaceModel) {
        devices = model.visibleDevices
        selectedID = model.selectedDevice?.id
        refreshing = model.deviceRefresh != nil
        cloudMode = model.cloudMode
        query = model.deviceQuery
        filtering = model.filteringDevices
    }
}

struct DevicesView: View {
    let state: DevicesState
    let width: Int
    let height: Int

    var body: some View {
        let index = state.devices.firstIndex { $0.id == state.selectedID } ?? 0
        let expanded = height >= 10
        let count = max(1, (height - 3) / (expanded ? 2 : 1))
        let start = max(0, min(index - count / 2, state.devices.count - count))
        Pane(
            title: "Devices", caption: state.devices.isEmpty ? "" : "\(index + 1)/\(state.devices.count)",
            focused: true, width: width, height: height
        ) {
            Text(hint).foregroundStyle(.info).lineLimit(1)
            if state.devices.isEmpty {
                Text(
                    !state.query.isEmpty
                        ? "No matches" : state.refreshing ? "Loading devices..." : "No simulators or devices found"
                ).foregroundStyle(.muted).lineLimit(1)
            }
            ForEach(Array(state.devices.dropFirst(start).prefix(count))) { device in
                VStack(alignment: .leading, spacing: 0) {
                    Text((device.id == state.selectedID ? "› " : "  ") + device.name + " · " + device.os)
                        .foregroundStyle(
                            device.id == state.selectedID ? SemanticShapeStyle.info : SemanticShapeStyle.foreground
                        )
                        .lineLimit(1)
                    if expanded {
                        Text("  \(device.kindLabel) · \(device.platform) · \(device.state)").foregroundStyle(.muted)
                            .lineLimit(1)
                    }
                }
                .frame(width: max(1, width - 4), alignment: .leading)
                .background(
                    device.id == state.selectedID ? SemanticShapeStyle.selection : SemanticShapeStyle.background)
            }
        }
    }

    private var hint: String {
        if state.filtering { return "/ " + state.query + "▏" }
        if !state.query.isEmpty { return "/ " + state.query + "  [/] Edit  [Esc] Clear" }
        return state.cloudMode
            ? "[/] Filter  [R] Refresh  [Esc] Close" : "[/] Filter  [n] New simulator  [R] Refresh  [Esc] Close"
    }
}

struct SimulatorNameView: View {
    let draft: SimulatorDraft
    let creating: Bool
    let width: Int
    let height: Int

    var body: some View {
        Pane(title: "New simulator · Name", focused: true, width: width, height: height) {
            Text(draft.runtime.name + " · " + draft.deviceType.name).foregroundStyle(.muted).lineLimit(1)
            Text("Name: " + String(draft.name.suffix(max(1, width - 12))) + "▏").foregroundStyle(.info).lineLimit(1)
            if let error = draft.error {
                Text(OutputFormatter.sanitize(error).replacingOccurrences(of: "\n", with: " ")).foregroundStyle(.danger)
                    .lineLimit(1)
            } else {
                Text(creating ? "Creating simulator..." : "Enter Create · Esc Cancel · Ctrl-U Clear").foregroundStyle(
                    .muted
                ).lineLimit(1)
            }
        }
    }
}
