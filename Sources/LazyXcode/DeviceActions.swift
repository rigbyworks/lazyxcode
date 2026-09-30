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
    var visibleDevices: [Destination] {
        guard !deviceQuery.isEmpty else { return managedDevices }
        return managedDevices.filter {
            "\($0.name) \($0.os) \($0.kindLabel) \($0.platform) \($0.state)".localizedCaseInsensitiveContains(
                deviceQuery)
        }
    }
    var selectedDevice: Destination? { selectedDevice(in: visibleDevices) }
    var deviceShortcuts: [String] {
        let close = deviceQuery.isEmpty ? "[Esc] Close" : "[Esc] Clear filter"
        return cloudMode
            ? ["[/] Filter", "[Enter] Details", "[R] Refresh", close]
            : ["[/] Filter", "[n] New simulator", "[Enter] Actions", "[R] Refresh", close]
    }

    // Callers that already filtered pass their list to avoid filtering again.
    func selectedDevice(in devices: [Destination]) -> Destination? {
        devices.first { $0.id == selectedDeviceID } ?? devices.first
    }

    func showDevices() {
        presentDevices()
        refreshDevices()
    }

    // Each opening starts unfiltered.
    private func presentDevices() {
        guard !showingDevices else { return }
        clearDeviceFilter()
        showingDevices = true
    }

    func clearDeviceFilter() {
        deviceQuery = ""
        filteringDevices = false
    }

    /// `note` leads the status line so an action's result survives the refresh it triggers.
    func refreshDevices(force: Bool = false, note: String? = nil) {
        guard !shuttingDown, simulatorCreation == nil else { return }
        if deviceRefresh != nil && !force { return }
        deviceRefresh?.cancel()
        deviceRefreshID = UUID()
        let token = deviceRefreshID
        let client = client
        func message(_ text: String) -> String { [note, text].compactMap { $0 }.joined(separator: " · ") }
        deviceStatus = message("Refreshing simulators and devices...")
        // devicectl can take its full timeout, so simulators must not wait for it.
        let simulators = refreshPart("Simulator refresh failed", token: token, fetch: client.simulatorInventory) {
            self.simulatorInventory = $0
        }
        let devices = refreshPart("Device refresh failed", token: token, fetch: client.physicalDevices) {
            self.physicalDevices = $0
        }
        simulatorRefresh = simulators
        deviceRefresh = Task {
            defer {
                if token == deviceRefreshID {
                    deviceRefresh = nil
                    simulatorRefresh = nil
                }
            }
            let errors = await withTaskCancellationHandler {
                [await simulators.value, await devices.value].compactMap { $0 }
            } onCancel: {
                simulators.cancel()
                devices.cancel()
            }
            guard !Task.isCancelled, token == deviceRefreshID else { return }
            deviceStatus =
                errors.isEmpty
                ? message(
                    "\(simulatorInventory?.devices.count ?? 0) simulators · \(physicalDevices.count) devices · [n] New simulator"
                )
                : errors.joined(separator: "\n")
        }
    }

    /// Returns a failure message; a cancelled or superseded refresh returns nil.
    private func refreshPart<Value: Sendable>(
        _ failure: String, token: UUID, fetch: @escaping @Sendable () async throws -> Value,
        apply: @escaping @MainActor (Value) -> Void
    ) -> Task<String?, Never> {
        Task {
            do {
                let value = try await fetch()
                guard !Task.isCancelled, token == deviceRefreshID else { return nil }
                apply(value)
                return nil
            } catch {
                guard !Task.isCancelled, token == deviceRefreshID else { return nil }
                return "\(failure): \(error.localizedDescription)"
            }
        }
    }

    func newSimulator() {
        guard !cloudMode, simulatorCreation == nil else { return }
        presentDevices()
        guard simulatorInventory != nil else {
            refreshDevices()
            load("Loading simulator runtimes...") {
                let failure = await self.simulatorRefresh?.value
                try Task.checkCancellation()
                guard self.simulatorInventory != nil else { throw AppError(failure ?? self.deviceStatus) }
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
                try Task.checkCancellation()
                simulatorDraft = nil
                selectedDeviceID = id
                clearDeviceFilter()
                // Finish creation before allowing refreshes or a second submission.
                simulatorCreation = nil
                guard !shuttingDown else { return }
                refreshDevices(force: true, note: "Created \(name)")
                destinationRefresh?.cancel()
                destinationRefresh = nil
                refreshDestinations(force: true)
            } catch {
                let cancelled = Task.isCancelled
                simulatorDraft?.error = cancelled ? "Cancelled" : error.localizedDescription
                guard cancelled else {
                    deviceStatus = "Could not create simulator: \(error.localizedDescription)"
                    return
                }
                // simctl may have finished before it was stopped.
                simulatorCreation = nil
                guard !shuttingDown else { return }
                refreshDevices(force: true, note: "Cancelled creating \(name)")
            }
        }
    }

    func cancelSimulatorCreation() {
        simulatorCreation?.cancel()
    }

    func openDeviceActions() {
        guard let device = selectedDevice else {
            if !cloudMode && managedDevices.isEmpty { newSimulator() }
            return
        }
        var actions: [MenuItem] = []
        if !cloudMode {
            actions.append(
                MenuItem("Use as build target") {
                    self.closeMenu()
                    // A recent destination query for this scheme needs no second query.
                    if self.destinationRefresh == nil, Date().timeIntervalSince(self.lastDestinationRefresh) < 15,
                        self.destinations.contains(where: { $0.id == device.id })
                    {
                        self.useAsBuildTarget(device)
                        return
                    }
                    let container = self.container
                    let scheme = self.scheme
                    self.load("Checking compatible destinations...") {
                        let destinations = try await self.client.destinations(container, scheme: scheme)
                        try Task.checkCancellation()
                        guard self.container == container, self.scheme == scheme else {
                            throw AppError("The scheme changed while checking \(device.name). Try again.")
                        }
                        guard destinations.contains(where: { $0.id == device.id }) else {
                            throw AppError(
                                "\(device.name) is not available for scheme \(self.scheme). Choose a compatible scheme or connect the device."
                            )
                        }
                        self.destinations = destinations
                        self.lastDestinationRefresh = Date()
                        self.discoveryCache?.saveDestinations(destinations, scheme: scheme)
                        self.useAsBuildTarget(device)
                    }
                })
            if device.isSimulator && device.state != "Unavailable" {
                actions.append(
                    MenuItem("Open simulator") {
                        self.closeMenu()
                        self.load("Opening \(device.name)...") {
                            try await self.client.boot(device)
                            try Task.checkCancellation()
                            self.status = "Opened \(device.name)"
                            self.refreshDevices(force: true, note: self.status)
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

    private func useAsBuildTarget(_ device: Destination) {
        chooseDestination(device.id)
        showingDevices = false
        pane = 0
        status = "Selected \(device.label)"
    }
}
