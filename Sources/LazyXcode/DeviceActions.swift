import Foundation
import LazyXcodeCore

struct SimulatorDraft {
    let runtime: SimulatorRuntime
    let deviceType: SimulatorDeviceType
    var name: String
    var error: String?
}

extension WorkspaceModel {
    var managedDevices: [Destination] { physicalDevices + (simulatorInventory?.devices ?? []) }
    var selectedDevice: Destination? { managedDevices.first { $0.id == selectedDeviceID } ?? managedDevices.first }

    func focusPane(_ value: Int) {
        pane = value
        if value == 3 { refreshDevices() }
    }

    func refreshDevices(force: Bool = false) {
        guard !shuttingDown, simulatorCreation == nil else { return }
        if deviceRefresh != nil && !force { return }
        deviceRefresh?.cancel()
        deviceRefreshID = UUID()
        let token = deviceRefreshID
        deviceStatus = "Refreshing simulators and devices..."
        deviceRefresh = Task {
            defer { if token == deviceRefreshID { deviceRefresh = nil } }
            var errors: [String] = []
            do {
                let inventory = try await client.simulatorInventory()
                guard !Task.isCancelled, token == deviceRefreshID else { return }
                simulatorInventory = inventory
            } catch {
                guard !Task.isCancelled, token == deviceRefreshID else { return }
                errors.append("Simulator refresh failed: \(error.localizedDescription)")
            }
            do {
                let devices = try await client.physicalDevices()
                guard !Task.isCancelled, token == deviceRefreshID else { return }
                physicalDevices = devices
            } catch {
                guard !Task.isCancelled, token == deviceRefreshID else { return }
                errors.append("Device refresh failed: \(error.localizedDescription)")
            }
            deviceStatus =
                errors.isEmpty
                ? "\(simulatorInventory?.devices.count ?? 0) simulators · \(physicalDevices.count) devices · [n] New simulator"
                : errors.joined(separator: "\n")
        }
    }

    func newSimulator() {
        guard !cloudMode, simulatorCreation == nil else { return }
        pane = 3
        guard simulatorInventory != nil else {
            refreshDevices()
            load("Loading simulator runtimes...") {
                await self.deviceRefresh?.value
                try Task.checkCancellation()
                guard self.simulatorInventory != nil else { throw AppError(self.deviceStatus) }
                self.newSimulator()
            }
            return
        }
        guard let inventory = simulatorInventory else { return }
        guard !inventory.runtimes.isEmpty else {
            deviceStatus = "No installed runtimes. Install one in Xcode Settings > Components, then press R."
            return
        }
        showMenu(
            "New simulator · Runtime",
            inventory.runtimes.map { runtime in
                MenuItem(runtime.name, id: runtime.id) { self.chooseSimulatorType(runtime) }
            })
    }

    func chooseSimulatorType(_ runtime: SimulatorRuntime) {
        guard !runtime.deviceTypes.isEmpty else {
            deviceStatus = "No supported device models for \(runtime.name). Choose another runtime."
            return
        }
        showMenu(
            "New simulator · Device model",
            runtime.deviceTypes.map { type in
                MenuItem(type.name, id: type.id) {
                    self.closeMenu()
                    self.simulatorDraft = SimulatorDraft(runtime: runtime, deviceType: type, name: type.name)
                }
            }, nested: true)
    }

    func submitSimulator() {
        guard !cloudMode, !shuttingDown, simulatorCreation == nil, let draft = simulatorDraft else { return }
        let name = draft.name.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty else {
            simulatorDraft?.error = "Enter a name"
            return
        }
        simulatorDraft?.error = nil
        deviceStatus = "Creating \(name)..."
        simulatorCreation = Task {
            defer { simulatorCreation = nil }
            do {
                let id = try await client.createSimulator(
                    name: name, deviceType: draft.deviceType, runtime: draft.runtime)
                simulatorDraft = nil
                selectedDeviceID = id
                // Finish creation before allowing refreshes or a second submission.
                simulatorCreation = nil
                guard !shuttingDown else { return }
                refreshDevices(force: true)
                deviceStatus = "Created \(name). Refreshing devices and build destinations..."
                destinationRefresh?.cancel()
                destinationRefresh = nil
                refreshDestinations(force: true)
            } catch {
                simulatorDraft?.error = error.localizedDescription
                deviceStatus = "Could not create simulator: \(error.localizedDescription)"
            }
        }
    }

    func openDeviceActions() {
        guard let device = selectedDevice else {
            if !cloudMode { newSimulator() }
            return
        }
        var actions: [MenuItem] = []
        if !cloudMode {
            actions.append(
                MenuItem("Use as build target") {
                    self.closeMenu()
                    let container = self.container
                    let scheme = self.scheme
                    self.load("Checking compatible destinations...") {
                        let destinations = try await self.client.destinations(container, scheme: scheme)
                        try Task.checkCancellation()
                        guard self.container == container, self.scheme == scheme else { return }
                        guard destinations.contains(where: { $0.id == device.id }) else {
                            throw AppError(
                                "\(device.name) is not available for scheme \(self.scheme). Choose a compatible scheme or connect the device."
                            )
                        }
                        self.destinations = destinations
                        self.discoveryCache?.saveDestinations(destinations, scheme: scheme)
                        self.chooseDestination(device.id)
                        self.pane = 0
                        self.status = "Selected \(device.label)"
                    }
                })
            if device.isSimulator && device.state != "Unavailable" {
                actions.append(
                    MenuItem("Open simulator") {
                        self.closeMenu()
                        self.load("Opening \(device.name)...") {
                            try await self.client.boot(device)
                            try Task.checkCancellation()
                            self.refreshDevices(force: true)
                            self.status = "Opened \(device.name)"
                        }
                    })
            }
        }
        actions.append(
            MenuItem("Show device details") {
                self.closeMenu()
                self.information()
            })
        showMenu(device.name, actions)
    }
}
