import Foundation
import LazyXcodeCore

extension WorkspaceModel {
    func testMenu() {
        guard !cloudMode else { return }
        guard discovery == nil else {
            status = "Wait for project discovery to finish"
            return
        }
        showMenu(
            "Run tests",
            [
                MenuItem("All tests") {
                    self.closeMenu()
                    self.queue(.test)
                },
                MenuItem("Unit tests") { self.runTestKind(ui: false) },
                MenuItem("UI tests") { self.runTestKind(ui: true) },
                MenuItem("Individual test...") {
                    self.closeMenu()
                    self.queue(.discoverTests, scope: "Discover tests")
                },
                MenuItem("Coverage: \(coverage ? "on" : "off")") {
                    self.toggleCoverage()
                    self.testMenu()
                },
            ])
    }
    func runTestKind(ui: Bool) {
        do {
            let targets = try client.testTargets(container, scheme: scheme).filter { $0.isUI == ui }.map(\.name)
            guard !targets.isEmpty else {
                throw AppError("No enabled \(ui ? "UI" : "unit") test targets in this scheme")
            }
            closeMenu()
            queue(.test, targets: targets, scope: ui ? "UI tests" : "Unit tests")
        } catch { status = error.localizedDescription }
    }
    func openDiscoveredTests(_ record: BuildRecord) {
        do {
            guard let path = record.enumerationPath else { throw AppError("No retained test enumeration") }
            let tests = try XcodeClient.enumeratedTests(Data(contentsOf: URL(fileURLWithPath: path)))
            guard !tests.isEmpty else { throw AppError("The scheme has no enabled tests") }
            showMenu(
                "Individual test",
                tests.map { identifier in
                    MenuItem(identifier) {
                        self.closeMenu()
                        self.queue(.test, targets: [identifier], scope: identifier, original: record)
                    }
                })
        } catch { status = error.localizedDescription }
    }
    func openTestActivity(_ record: BuildRecord) {
        guard !record.phase.active else {
            status = "This activity is still running"
            return
        }
        if record.operation == .discoverTests {
            openDiscoveredTests(record)
            return
        }
        guard let path = record.resultBundlePath, FileManager.default.fileExists(atPath: path) else {
            status = "This activity has no retained result bundle"
            return
        }
        resultMenu(path: path, record: record)
    }
    func resultMenu(path: String, record: BuildRecord?) {
        var items = [
            MenuItem("Browse test results") { self.browseTests(path: path, record: record) },
            MenuItem("Browse coverage") { self.browseCoverage(path: path) },
            MenuItem("Open result bundle in Xcode") {
                self.closeMenu()
                self.load("Opening result bundle...") { try await self.client.runner.run("open", [path]) }
            },
        ]
        if let record, !cloudMode {
            items += [
                MenuItem("Rerun failed tests") { self.rerunFailed(path: path, record: record) },
                MenuItem("Compare coverage with an earlier run") { self.coverageBaselines(record) },
            ]
        }
        showMenu("Test activity", items)
    }
    func browseTests(path: String, record: BuildRecord?) {
        load("Reading test results...") {
            let tests = try await self.client.testResults(path)
            try Task.checkCancellation()
            guard !tests.isEmpty else { throw AppError("No tests recorded in this bundle") }
            self.showMenu(
                "Test results",
                tests.map { test in
                    MenuItem("\(test.result) · \(test.name) · \(test.duration)", id: test.id) {
                        self.showTest(test, path: path, record: record)
                    }
                }, nested: true)
        }
    }
    func showTest(_ test: TestCase, path: String, record: BuildRecord?) {
        load("Reading test details...") {
            let text = try await self.client.testDetails(path, id: test.id)
            try Task.checkCancellation()
            self.closeMenu()
            self.showDetail(text, actions: self.testActions(test, path: path, record: record))
        }
    }
    func testActions(_ test: TestCase, path: String, record: BuildRecord?) -> [MenuItem] {
        var items = [
            MenuItem("Test details") { self.showTest(test, path: path, record: record) },
            MenuItem("Activities") {
                self.load("Reading test activities...") {
                    let text = try await self.client.testDetails(path, id: test.id, activities: true)
                    try Task.checkCancellation()
                    self.closeMenu()
                    self.showDetail(text, actions: self.testActions(test, path: path, record: record))
                }
            },
            MenuItem("Export attachments") {
                self.load("Exporting attachments...") {
                    let directory = try await self.client.exportAttachments(path, id: test.id)
                    try Task.checkCancellation()
                    let files =
                        FileManager.default.enumerator(at: directory, includingPropertiesForKeys: [.isRegularFileKey])?
                        .allObjects.compactMap { $0 as? URL }.filter {
                            (try? $0.resourceValues(forKeys: [.isRegularFileKey]).isRegularFile) == true
                                && $0.pathExtension != "json"
                        } ?? []
                    self.showMenu(
                        "Attachments",
                        files.map { file in
                            MenuItem(file.lastPathComponent, id: file.path) {
                                self.closeMenu()
                                self.load("Opening attachment...") {
                                    try await self.client.runner.run("open", [file.path])
                                }
                            }
                        }, nested: true)
                    self.status =
                        files.isEmpty
                        ? "No attachments recorded. Export directory: \(directory.path)"
                        : "Attachments: \(directory.path)"
                }
            },
            MenuItem("Back to test results") { self.browseTests(path: path, record: record) },
        ]
        if let record, !cloudMode, !test.identifier.isEmpty {
            items.append(
                MenuItem("Run this test") {
                    self.closeMenu()
                    self.queue(.test, targets: [test.identifier], scope: test.identifier, original: record)
                })
        }
        return items
    }
    func rerunFailed(path: String, record: BuildRecord) {
        load("Finding failed tests...") {
            let tests = try await self.client.testResults(path).filter(\.failed)
            try Task.checkCancellation()
            let identifiers = tests.map(\.identifier).filter { !$0.isEmpty }
            guard !identifiers.isEmpty else {
                throw AppError("No failed tests with runnable identifiers. No tests were started.")
            }
            self.closeMenu()
            self.queue(.test, targets: identifiers, scope: "Failed tests", original: record)
        }
    }
    func browseCoverage(path: String) {
        load("Reading coverage...") {
            let report = try await self.client.coverage(path)
            try Task.checkCancellation()
            self.coverageMenu(report, title: "Coverage")
        }
    }
    func coverageMenu(_ report: JSONValue, title: String) {
        var items = [
            MenuItem("Show complete report") {
                self.closeMenu()
                self.showDetail(
                    XcodeClient.coverageText(report),
                    actions: [MenuItem("Back to coverage") { self.coverageMenu(report, title: title) }])
            }
        ]
        let children = ["targets", "files", "functions"].flatMap { report[$0].array }
        items += children.enumerated().map { index, child in
            let label = child["path"].string.isEmpty ? child["name"].string : child["path"].string
            return MenuItem(String(format: "%.2f%% · %@", child["lineCoverage"].number * 100, label), id: "\(index)") {
                if child["files"].array.isEmpty && child["functions"].array.isEmpty {
                    self.closeMenu()
                    self.showDetail(
                        XcodeClient.coverageText(child),
                        actions: [MenuItem("Back to " + title) { self.coverageMenu(report, title: title) }])
                } else {
                    self.coverageMenu(child, title: label)
                }
            }
        }
        showMenu(title, items, nested: true)
    }
    func coverageBaselines(_ record: BuildRecord) {
        let baselines = records.filter {
            $0.id != record.id && $0.container == record.container && $0.scheme == record.scheme
                && $0.simulator.id == record.simulator.id
                && $0.startedAt < record.startedAt && $0.coverage == true && $0.resultBundlePath != nil
                && !$0.phase.active
        }
        guard !baselines.isEmpty else {
            status = "No earlier coverage run for this scheme and destination"
            return
        }
        showMenu(
            "Coverage baseline",
            baselines.map { baseline in
                MenuItem("\(baseline.startedAt.formatted()) · \(baseline.testScope ?? "All tests")", id: baseline.id) {
                    self.load("Comparing coverage...") {
                        let change = try await self.client.compareCoverage(
                            record.resultBundlePath!, baseline: baseline.resultBundlePath!)
                        try Task.checkCancellation()
                        self.closeMenu()
                        self.showDetail(
                            "Coverage change from \(baseline.startedAt.formatted()) · \(baseline.testScope ?? "All tests")\n\n"
                                + XcodeClient.coverageDifferenceText(change))
                    }
                }
            }, nested: true)
    }
}
