import Foundation

extension CloudResource {
    public var statusLabel: String {
        switch status.uppercased() {
        case "PENDING", "NOT_STARTED": "WAIT"
        case "RUNNING": "RUN"
        case "SUCCEEDED", "PASSED": "OK"
        case "FAILED", "ERRORED": "FAIL"
        case "CANCELED", "CANCELLED": "STOP"
        case "SKIPPED": "SKIP"
        default: status.isEmpty ? "WAIT" : status
        }
    }
    public var isLog: Bool {
        attributes["fileType"].string.localizedCaseInsensitiveContains("log")
            || ["log", "txt"].contains(URL(fileURLWithPath: name).pathExtension.lowercased())
    }
    public var logFingerprint: String { id + ":" + String(Int64(attributes["fileSize"].number)) }
    public func duration(now: Date = Date()) -> TimeInterval {
        func date(_ key: String) -> Date? {
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            return formatter.date(from: attributes[key].string)
                ?? ISO8601DateFormatter().date(from: attributes[key].string)
        }
        guard let start = date("startedDate") ?? date("createdDate") else { return 0 }
        return max(0, (date("finishedDate") ?? (active ? now : start)).timeIntervalSince(start))
    }
}

extension CloudPage {
    /// Polling refreshes the first page without losing already loaded history.
    public mutating func merge(_ page: CloudPage, older: Bool) {
        if older {
            let existing = Set(items.map(\.id))
            items += page.items.filter { !existing.contains($0.id) }
            next = page.next
            hasOlderPages = true
        } else {
            let fresh = Set(page.items.map(\.id))
            items = page.items + items.filter { !fresh.contains($0.id) }
            if !hasOlderPages { next = page.next }
        }
        let newReferences = Set(page.included.map { $0.value["type"].string + ":" + $0.id })
        included = page.included + included.filter { !newReferences.contains($0.value["type"].string + ":" + $0.id) }
    }
}

public struct ExpiredArtifactURL: Error, Sendable {}

extension CloudClient {
    public func download(_ artifact: CloudResource, for run: CloudResource, page: CloudPage, to destination: URL)
        async throws -> URL
    {
        do { return try await download(artifact, to: destination) } catch is ExpiredArtifactURL {
            let fresh = try await details(run, page: page)
            guard let replacement = fresh.artifacts.first(where: { $0.id == artifact.id }) else {
                throw AppError("Artifact is no longer available in this Cloud run")
            }
            return try await download(replacement, to: destination)
        }
    }
}

extension CloudDetails {
    static func format(
        run: CloudResource, page: CloudPage,
        actions: [(CloudResource, [CloudResource], [CloudResource], [CloudResource])]
    ) -> CloudDetails {
        var lines = ["XCODE CLOUD BUILD #\(Int(run.attributes["number"].number))"]
        lines.append("Workflow       " + (page.related(run, "workflow")?.name ?? "-"))
        for (label, relationship, commitKey) in [
            ("Source", "sourceBranchOrTag", "sourceCommit"), ("Destination", "destinationBranch", "destinationCommit"),
        ] {
            let branch = page.related(run, relationship)?.name ?? ""
            let sha = String(run.attributes[commitKey]["commitSha"].string.prefix(7))
            if !branch.isEmpty || !sha.isEmpty {
                lines.append(
                    label.padding(toLength: 15, withPad: " ", startingAt: 0)
                        + [branch, sha].filter { !$0.isEmpty }.joined(separator: " · "))
            }
        }
        let message = run.attributes["sourceCommit"]["message"].string.components(separatedBy: .newlines).first ?? ""
        if !message.isEmpty { lines.append("Commit         " + message) }
        let reason = run.attributes["startReason"].string.replacingOccurrences(of: "_", with: " ").lowercased()
        if !reason.isEmpty {
            lines.append(
                "Reason         " + reason + (run.attributes["isPullRequestBuild"].bool ? " (pull request)" : ""))
        }
        let started =
            run.attributes["startedDate"].string.isEmpty
            ? run.attributes["createdDate"].string : run.attributes["startedDate"].string
        if !started.isEmpty { lines.append("Started        " + started) }
        lines.append("Status         \(run.statusLabel) · \(BuildOutput.duration(run.duration()))")
        lines += ["", "ACTIONS (\(actions.count))"]
        var diagnostics: [String] = []
        var seen = Set<String>()
        var results: [CloudResource] = []
        var artifacts: [CloudResource] = []
        for (action, issues, tests, files) in actions {
            let marker = ["OK": "✓", "FAIL": "✗", "RUN": "●"][action.statusLabel] ?? "○"
            lines.append(
                "  \(marker) \(action.name)  \(action.statusLabel)  \(BuildOutput.duration(action.duration()))")
            for issue in issues {
                let attr = issue.attributes
                let source = attr["fileSource"]
                let path = source["path"].string
                let location =
                    path.isEmpty
                    ? "Build" : "\(URL(fileURLWithPath: path).lastPathComponent):\(Int(source["lineNumber"].number))"
                let text = "  \(attr["issueType"].string.lowercased()): \(location) — \(attr["message"].string)"
                if seen.insert(text).inserted { diagnostics.append(text) }
            }
            results += tests
            artifacts += files
        }
        if !diagnostics.isEmpty { lines += ["", "DIAGNOSTICS (\(diagnostics.count))"] + diagnostics }
        if actions.contains(where: { $0.0.attributes["actionType"].string == "TEST" }) {
            let passed = results.filter { ["PASSED", "SUCCESS", "SUCCEEDED"].contains($0.attributes["status"].string) }
                .count
            let skipped = results.filter { $0.attributes["status"].string == "SKIPPED" }.count
            let failures = results.filter {
                !["PASSED", "SUCCESS", "SUCCEEDED", "SKIPPED"].contains($0.attributes["status"].string)
            }
            lines += [
                "", "TESTS",
                results.isEmpty
                    ? "  Waiting for test results..."
                    : "  \(passed) passed, \(failures.count) failed, \(skipped) skipped",
            ]
            lines += failures.map {
                "  ✗ \($0.attributes["className"].string).\($0.name)  \(BuildOutput.duration($0.attributes["duration"].number))"
            }
        }
        lines += ["", "ARTIFACTS (\(artifacts.count))"]
        lines += artifacts.map {
            "  \($0.name)  \(ByteCountFormatter.string(fromByteCount: Int64($0.attributes["fileSize"].number), countStyle: .file))"
        }
        if artifacts.isEmpty {
            lines.append(run.active ? "  Artifacts appear when actions finish" : "  No artifacts available")
        }
        lines += ["", "[a] Download artifact  [v] Raw logs  [Enter] Test results"]
        return CloudDetails(text: lines.joined(separator: "\n"), artifacts: artifacts)
    }
}
