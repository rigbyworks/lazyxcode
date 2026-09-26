import Foundation
import LazyXcodeCore

extension WorkspaceModel {
    func toggleMode() {
        cancelPending()
        closeMenu()
        let current = OutputPosition(
            offset: outputOffset, follow: follow, detail: detailText, actions: detailActions, paused: pausedOutput)
        if cloudMode { savedCloudOutput = current } else { savedLocalOutput = current }
        cloudMode.toggle()
        let restored = cloudMode ? savedCloudOutput : savedLocalOutput
        detailText = restored.detail
        detailActions = restored.actions
        pausedOutput = restored.paused
        outputOffset = restored.offset
        follow = restored.follow
        buildRow = 0
        if cloudMode { refreshCloud() }
    }
    func refreshCloud(older: Bool = false) {
        guard cloudMode, !cloudRefreshing else { return }
        let token = cloudGeneration
        let advance = older && selectedCloud?.id == cloudPage.items.last?.id
        cloudRefreshing = true
        cloudLastRefresh = Date()
        cloudRefresh = Task {
            defer {
                if token == cloudGeneration {
                    cloudRefreshing = false
                    cloudRefresh = nil
                }
            }
            do {
                let cloud: CloudClient
                if let cloudClient {
                    cloud = cloudClient
                } else {
                    cloud = CloudClient(credentials: try CloudCredentials.load())
                    cloudClient = cloud
                }
                if cloudProducts.isEmpty {
                    let products = try await cloud.products()
                    try Task.checkCancellation()
                    guard token == cloudGeneration else { return }
                    cloudProducts = products
                    if !products.contains(where: { $0.id == cloudProduct }) {
                        cloudProduct = products.count == 1 ? products[0].id : ""
                        cloudWorkflow = ""
                    }
                }
                guard !cloudProduct.isEmpty else {
                    cloudStatus =
                        cloudProducts.isEmpty ? "No Xcode Cloud products visible to this key" : "Choose a product"
                    if cloudMode && menu == nil { openCloudProducts() }
                    return
                }
                let workflows = try await cloud.workflows(cloudProduct)
                try Task.checkCancellation()
                guard token == cloudGeneration else { return }
                cloudWorkflows = workflows
                if !workflows.contains(where: { $0.id == cloudWorkflow }) { cloudWorkflow = "" }
                let page = try await cloud.runs(
                    product: cloudProduct, workflow: cloudWorkflow, cursor: older ? cloudPage.next : "")
                try Task.checkCancellation()
                guard token == cloudGeneration else { return }
                cloudPage.merge(page, older: older)
                if !cloudPage.items.contains(where: { $0.id == selectedCloudID }) {
                    selectedCloudID = cloudPage.items.first?.id
                }
                if advance, let id = page.items.first?.id { selectedCloudID = id }
                cloudStatus = "Connected · refreshed \(Date().formatted(date: .omitted, time: .standard))"
                if let warning = await cloud.credentials.warning { cloudStatus += "\n" + warning }
                preferences.cloudProducts[container.path] = cloudProduct
                preferences.cloudWorkflows[container.path] = cloudWorkflow
                savePreferences()
                if cloudMode, let id = selectedCloud?.id, !loading { loadCloudDetails(id) }
            } catch {
                if !Task.isCancelled && token == cloudGeneration {
                    cloudStatus = error.localizedDescription + (cloudPage.items.isEmpty ? "" : "\nShowing stale data")
                    status = cloudStatus
                }
            }
        }
    }
    func selectCloud(_ id: String) {
        cancelPending()
        selectedCloudID = id
        detailText = nil
        detailActions = []
        pausedOutput = nil
        outputOffset = 0
        follow = true
        loadCloudDetails(id)
    }
    func loadCloudDetails(_ id: String) {
        guard let cloud = cloudClient, let run = cloudPage.items.first(where: { $0.id == id }) else { return }
        let page = cloudPage
        load("Loading Cloud build details...") {
            let details = try await cloud.details(run, page: page)
            try Task.checkCancellation()
            let previous = self.cloudDetails[id]?.artifacts.filter(\.isLog).map(\.logFingerprint)
            let updated = details.artifacts.filter(\.isLog).map(\.logFingerprint)
            if previous != updated { self.cloudLogs[id] = nil }
            self.cloudDetails[id] = details
            self.status = "Cloud build #\(Int(run.attributes["number"].number))"
            if self.cloudRaw && self.cloudMode && self.selectedCloud?.id == id { self.loadCloudLogs() }
        }
    }
    func openCloudSetting() {
        switch buildRow {
        case 0: openCloudProducts()
        case 1:
            let all = MenuItem("All workflows") { self.chooseCloudWorkflow("") }
            showMenu(
                "Workflow",
                [all]
                    + cloudWorkflows.map { workflow in
                        MenuItem(workflow.name, id: workflow.id) { self.chooseCloudWorkflow(workflow.id) }
                    })
        default: showDetail(cloudInformation)
        }
    }
    func openCloudProducts() {
        showMenu(
            "Cloud product",
            cloudProducts.map { product in
                MenuItem(product.name, id: product.id) {
                    self.closeMenu()
                    self.cloudProduct = product.id
                    self.cloudWorkflow = ""
                    self.resetCloudSelection()
                    self.refreshCloud()
                }
            })
    }
    func chooseCloudWorkflow(_ id: String) {
        closeMenu()
        cloudWorkflow = id
        resetCloudSelection()
        refreshCloud()
    }
    func resetCloudSelection() {
        cancelPending()
        cloudRefresh?.cancel()
        cloudRefresh = nil
        cloudRefreshing = false
        cloudGeneration = UUID()
        cloudPage = CloudPage(items: [], included: [], next: "")
        selectedCloudID = nil
        cloudDetails = [:]
        cloudLogs = [:]
        detailText = nil
        detailActions = []
        outputOffset = 0
    }
    func openArtifacts(resultsOnly: Bool = false) {
        guard let run = selectedCloud, let details = cloudDetails[run.id] else {
            status = "Wait for build details to load"
            return
        }
        let artifacts = details.artifacts.filter {
            !resultsOnly || $0.attributes["fileType"].string.localizedCaseInsensitiveContains("result")
                || $0.name.localizedCaseInsensitiveContains("xcresult")
        }
        guard !artifacts.isEmpty else {
            status = resultsOnly ? "No test-result artifacts in this run" : "No artifacts in this run"
            return
        }
        showMenu(
            resultsOnly ? "Test-result artifact" : "Download artifact",
            artifacts.map { artifact in
                MenuItem("\(artifact.name) · \(Int(artifact.attributes["fileSize"].number)) bytes", id: artifact.id) {
                    self.downloadArtifact(artifact, run: run, inspect: resultsOnly)
                }
            })
    }
    func downloadArtifact(_ artifact: CloudResource, run: CloudResource, inspect: Bool) {
        guard let cloud = cloudClient, let store = manager?.store else { return }
        load("Downloading \(artifact.name)... Esc or x cancels") {
            let destination = try store.artifactURL(run: run.id, artifact: artifact.id, name: artifact.name)
            let file = try await cloud.download(artifact, for: run, page: self.cloudPage, to: destination)
            try Task.checkCancellation()
            self.closeMenu()
            self.status = "Downloaded \(file.path)"
            if inspect {
                let result = try await ResultArchive.expandAsync(file)
                try Task.checkCancellation()
                self.resultMenu(path: result.path, record: nil)
            } else {
                self.showDetail("Downloaded \(artifact.name)\n\n\(file.path)")
            }
        }
    }
    func toggleOutput() {
        pausedOutput = nil
        detailText = nil
        detailActions = []
        outputOffset = 0
        follow = true
        guard cloudMode else {
            raw.toggle()
            pageEnd = nil
            newerPages = []
            outputVersion = -1
            tick()
            return
        }
        cloudRaw.toggle()
        loadCloudLogs()
    }
    func loadCloudLogs() {
        guard cloudRaw, let run = selectedCloud, cloudLogs[run.id] == nil,
            let details = cloudDetails[run.id], let cloud = cloudClient, let store = manager?.store
        else { return }
        let artifacts = details.artifacts.filter(\.isLog)
        load("Downloading build logs...") {
            var text = ""
            var remainingFiles = 64
            var remainingBytes = 256 * 1024
            var truncated = false
            for artifact in artifacts {
                if remainingFiles == 0 || remainingBytes == 0 {
                    truncated = true
                    break
                }
                let path = try store.artifactURL(run: run.id, artifact: artifact.id, name: artifact.name)
                let file = try await cloud.download(artifact, for: run, page: self.cloudPage, to: path)
                try Task.checkCancellation()
                var source = file
                if file.pathExtension.lowercased() == "zip" {
                    source = try await ResultArchive.expandAsync(file, requireResult: false)
                    try Task.checkCancellation()
                }
                let excerpt = try await CloudLogReader.read(
                    source, maxFiles: remainingFiles, maxBytes: remainingBytes)
                try Task.checkCancellation()
                remainingFiles -= excerpt.filesRead
                remainingBytes -= excerpt.bytesRead
                truncated = truncated || excerpt.truncated
                text += excerpt.text
            }
            if truncated { text += "\n\nEarlier log content omitted. Open the downloaded artifact for complete logs." }
            self.cloudLogs[run.id] = text.isEmpty ? "No log artifacts available" : text
        }
    }
}
