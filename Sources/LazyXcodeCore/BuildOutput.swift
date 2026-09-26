import Foundation

/// Incremental summaries are independent of the bounded raw/console windows.
/// Progress markers use the Go log format so timings survive a restart.
public struct BuildOutput: Sendable {
    private struct Step: Sendable {
        var duration: TimeInterval = 0
        var started: Date?
    }
    private struct Suite: Sendable {
        var status = "running"
        var current = ""
        var passed = 0
        var failed = 0
        var duration: TimeInterval = 0
    }
    private static let definitions: [(String, [String])] = [
        (
            "Resolve packages",
            ["Resolve Package Graph", "Prepare packages", "ComputePackagePrebuildTargetDependencyGraph"]
        ),
        ("Plan build", ["ComputeTargetDependencyGraph"]),
        ("Prepare build", ["CreateBuildDescription", "CreateBuildDirectory"]),
        (
            "Compile resources",
            ["CompileAssetCatalog", "CompileStoryboard", "CompileXIB", "CompileMetalFile", "ProcessXCFramework"]
        ),
        (
            "Compile sources",
            ["SwiftDriver", "SwiftCompile", "SwiftEmitModule", "CompileSwift", "CompileC", "CompilePrecompiledHeader"]
        ),
        ("Link", ["Ld ", "Libtool ", "CreateUniversalBinary"]),
        ("Package", ["CopySwiftLibs", "ExtractAppIntentsMetadata", "GenerateDSYMFile", "Touch "]),
        ("Sign", ["CodeSign "]),
        ("Validate", ["Validate ", "RegisterExecutionPolicyException", "RegisterWithLaunchServices"]),
    ]
    private var pending = Data()
    private var longLine = false
    private var diagnostics: [String] = []
    private var seenDiagnostics = Set<String>()
    private var steps: [Int: Step] = [:]
    private var currentStep: Int?
    private var recordedProgress = false
    private var suites: [String: Suite] = [:]
    private var suiteOrder: [String] = []
    private var outcome = ""
    private var deployment: [String] = []
    private var console: [String] = []
    private var consoleBytes = 0
    private var running = false
    private var finished = false
    private var testing = false
    private var summaryLimited = false
    private var consoleLimited = false
    public init() {}

    /// Returns timing records to persist alongside the original bytes.
    @discardableResult
    public mutating func append(_ data: Data, at now: Date = Date()) -> Data {
        var markers = ""
        for byte in data {
            if byte == 10 {
                markers += consume(String(decoding: pending, as: UTF8.self), at: now)
                pending.removeAll(keepingCapacity: true)
                longLine = false
            } else if pending.count < 4096 {
                pending.append(byte)
            } else {
                longLine = true
            }
        }
        return Data(markers.utf8)
    }

    @discardableResult
    public mutating func finish(at now: Date = Date()) -> Data {
        var markers = ""
        if !pending.isEmpty {
            markers += consume(String(decoding: pending, as: UTF8.self), at: now)
            pending.removeAll()
        }
        markers += finishStep(at: now)
        finished = true
        return Data(markers.utf8)
    }

    private mutating func finishStep(at now: Date) -> String {
        guard let index = currentStep, let start = steps[index]?.started else { return "" }
        steps[index]!.duration += max(0, now.timeIntervalSince(start))
        steps[index]!.started = nil
        currentStep = nil
        guard !recordedProgress else { return "" }
        return "[lazyxcode:step] done \(Int(steps[index]!.duration * 1000)) \(index) \(Self.definitions[index].0)\n"
    }

    private mutating func consume(_ raw: String, at now: Date) -> String {
        let line = raw.replacingOccurrences(of: "\u{1B}\\[[0-?]*[ -/]*[@-~]", with: "", options: .regularExpression)
            .trimmingCharacters(in: .whitespacesAndNewlines)
        if line.hasPrefix("[lazyxcode:step]") {
            let parts = line.split(separator: " ", maxSplits: 4)
            if parts.count == 5, let value = Double(parts[2]), let index = Int(parts[3]),
                Self.definitions.indices.contains(index)
            {
                recordedProgress = true
                if parts[1] == "start" {
                    steps[index, default: Step()].started = Date(timeIntervalSince1970: value / 1000)
                    currentStep = index
                } else if parts[1] == "done" {
                    steps[index] = Step(duration: value / 1000)
                    if currentStep == index { currentStep = nil }
                }
            }
            return ""
        }
        if line.hasPrefix("[lazyxcode ") || line.hasPrefix("[lazyxcode]") {
            if line.contains("] running") || line == "[lazyxcode] App console" {
                let marker = finishStep(at: now)
                running = true
                return marker
            }
            if line.contains("] testing") { testing = true }
            if line.contains("] succeeded") || line.contains("] cancelled") || line.contains("] build_failed")
                || line.contains("] test_failed") || line.contains("] run_failed")
            {
                finished = true
                return finishStep(at: now)
            }
            if ["booting", "installing", "launching", "Build succeeded;", "Installing app", "Launching "].contains(
                where: line.contains)
            {
                if deployment.count < 8 { deployment.append(line) }
                return finishStep(at: now)
            }
            return ""
        }
        if running {
            console.append(raw)
            consoleBytes += raw.utf8.count + 1
            consoleLimited = consoleLimited || longLine
            while console.count > 2000 || consoleBytes > 256 * 1024 {
                consoleBytes -= console.removeFirst().utf8.count + 1
                consoleLimited = true
            }
            return ""
        }
        if let diagnostic = Self.diagnostic(line) {
            if seenDiagnostics.insert(diagnostic).inserted {
                if diagnostics.count < 256 {
                    diagnostics.append(diagnostic)
                } else {
                    summaryLimited = true
                    seenDiagnostics.remove(diagnostic)
                }
            }
            summaryLimited = summaryLimited || longLine
        }
        if line.hasPrefix("** ") || line.hasPrefix("✔ Test run with") || line.hasPrefix("✘ Test run with") {
            outcome = line
            return finishStep(at: now)
        }
        if line.hasPrefix("Test Suite '") || line.hasPrefix("Test Case '") || line.hasPrefix("◇")
            || line.hasPrefix("◆") || line.hasPrefix("✔") || line.hasPrefix("✘")
        {
            testing = true
            observeTest(line)
            return finishStep(at: now)
        }
        guard !recordedProgress, !finished,
            let index = Self.definitions.firstIndex(where: { $0.1.contains(where: line.hasPrefix) }),
            index != currentStep
        else { return "" }
        let marker = finishStep(at: now)
        steps[index, default: Step()].started = now
        currentStep = index
        return marker
            + "[lazyxcode:step] start \(Int(now.timeIntervalSince1970 * 1000)) \(index) \(Self.definitions[index].0)\n"
    }

    private mutating func updateSuite(_ name: String, _ update: (inout Suite) -> Void) {
        if suites[name] == nil {
            guard suites.count < 256 else {
                summaryLimited = true
                return
            }
            suiteOrder.append(name)
            suites[name] = Suite()
        }
        update(&suites[name]!)
    }
    private mutating func observeTest(_ line: String) {
        if let match = Self.matches(#"^Test Suite '(.+)' (started|passed|failed)"#, line) {
            updateSuite(match[0]) { $0.status = match[1] == "started" ? "running" : match[1] }
        } else if let match = Self.matches(
            #"^Test Case '[-+]\[(?:[^.]+\.)?([^ ]+) ([^]]+)\]' (started|passed|failed)(?: \(([0-9.]+) seconds\))?"#,
            line)
        {
            updateSuite(match[0]) { suite in
                suite.current = match[2] == "started" ? match[1] : ""
                if match[2] == "passed" { suite.passed += 1 }
                if match[2] == "failed" { suite.failed += 1 }
                if match.count > 3 { suite.duration += Double(match[3]) ?? 0 }
            }
        } else if let match = Self.matches(
            #"^[◇◆✔✘] Suite "?(.+?)"? (started|passed|failed)(?: after ([0-9.]+) seconds)?"#, line)
        {
            updateSuite(match[0]) { suite in
                suite.status = match[1] == "started" ? "running" : match[1]
                if match.count > 2 { suite.duration = Double(match[2]) ?? 0 }
            }
        } else if let match = Self.matches(#"^✘ Test .+ recorded an issue at (.+):(\d+):(\d+):\s+(.+)$"#, line) {
            let diagnostic =
                "error: \(URL(fileURLWithPath: match[0]).lastPathComponent):\(match[1]):\(match[2]) — \(match[3])"
            if diagnostics.count < 256, seenDiagnostics.insert(diagnostic).inserted { diagnostics.append(diagnostic) }
        }
    }

    private static func matches(_ pattern: String, _ text: String) -> [String]? {
        guard let regex = try? NSRegularExpression(pattern: pattern),
            let match = regex.firstMatch(in: text, range: NSRange(text.startIndex..., in: text))
        else { return nil }
        return (1..<match.numberOfRanges).map { index in
            Range(match.range(at: index), in: text).map { String(text[$0]) } ?? ""
        }
    }
    private static func diagnostic(_ line: String) -> String? {
        let lower = line.lowercased()
        guard
            lower.contains("error") || lower.contains("warning") || lower.hasPrefix("ld:")
                || lower.contains("undefined symbols") || lower.contains("duplicate symbol")
                || lower.contains("nonzero exit code") || lower.contains("build commands failed")
        else { return nil }
        if let match = matches(#"^(.+):(\d+):(\d+):\s+(fatal error|error|warning):\s+(.+)$"#, line) {
            return
                "\(match[3]): \(URL(fileURLWithPath: match[0]).lastPathComponent):\(match[1]):\(match[2]) — \(match[4])"
        }
        if lower.hasPrefix("ld:") { return "error: Linker — \(line.dropFirst(3).trimmingCharacters(in: .whitespaces))" }
        for fragment in [
            "undefined symbols for architecture", "duplicate symbol", "failed with a nonzero exit code",
            "the following build commands failed:",
        ] where lower.contains(fragment) {
            return "error: Build — \(line)"
        }
        for severity in ["fatal error", "error", "warning"] {
            if let range = lower.range(of: severity + ":"),
                range.lowerBound == lower.startIndex
                    || [" ", ":"].contains(lower[lower.index(before: range.lowerBound)])
            {
                return "\(severity): Build — \(line[range.upperBound...].trimmingCharacters(in: .whitespaces))"
            }
        }
        return nil
    }

    public static func duration(_ value: TimeInterval) -> String {
        let value = max(0, value)
        if value < 1 { return "\(Int((value * 1000).rounded()))ms" }
        if value < 60 { return String(format: "%.1fs", value) }
        return String(format: "%dm%02ds", Int(value) / 60, Int(value) % 60)
    }
    public func text(now: Date = Date(), phase: Phase? = nil) -> String {
        var lines: [String] = []
        if !steps.isEmpty {
            lines.append(testing ? "BUILD PREPARATION" : "BUILD STEPS")
            for index in steps.keys.sorted() {
                let step = steps[index]!
                let active = step.started != nil && !finished && phase?.active != false
                let elapsed = active ? max(0, now.timeIntervalSince(step.started!)) : 0
                let name = Self.definitions[index].0.padding(toLength: 23, withPad: " ", startingAt: 0)
                lines.append("  \(active ? "●" : "✓") \(name) \(Self.duration(step.duration + elapsed))")
            }
        }
        if testing {
            let visible = suiteOrder.filter { name in
                let aggregate =
                    ["all tests", "selected tests"].contains(name.lowercased()) || name.hasSuffix(".xctest")
                return !aggregate || suites[name]!.passed + suites[name]!.failed > 0
            }
            if !lines.isEmpty { lines.append("") }
            lines.append("TEST SUITES (\(visible.count))")
            if visible.isEmpty { lines.append("  ● Waiting for test suites...") }
            for name in visible {
                let suite = suites[name]!
                let complete = finished || phase?.active == false
                let status =
                    suite.failed > 0
                    ? "failed"
                    : suite.status == "running" && complete
                        ? (phase == .succeeded ? "passed" : "stopped") : suite.status
                let marker = status == "failed" ? "✗" : status == "passed" ? "✓" : status == "stopped" ? "○" : "●"
                let counts =
                    suite.passed + suite.failed > 0
                    ? "\(suite.passed) passed" + (suite.failed > 0 ? ", \(suite.failed) failed" : "") : status
                lines.append(
                    "  \(marker) \(name)  \(counts)" + (suite.duration > 0 ? "  \(Self.duration(suite.duration))" : ""))
                if !suite.current.isEmpty && !complete { lines.append("      ↳ \(suite.current)") }
            }
        }
        if !diagnostics.isEmpty { lines += ["", "DIAGNOSTICS (\(diagnostics.count))"] + diagnostics.map { "  " + $0 } }
        if !deployment.isEmpty { lines += ["", "DEPLOYMENT"] + deployment.map { "  " + $0 } }
        if !outcome.isEmpty { lines += ["", outcome] }
        if summaryLimited { lines.append("[Summary limit reached; see the complete log for remaining details.]") }
        if running {
            lines += ["", "APP CONSOLE"]
            if consoleLimited || longLine {
                lines.append("[Earlier console output omitted; browse the complete log with [ / ].]")
            }
            lines += console
            if !pending.isEmpty { lines.append(String(decoding: pending, as: UTF8.self)) }
            if console.isEmpty && pending.isEmpty { lines.append("  Waiting for app output...") }
        }
        return lines.isEmpty
            ? "Waiting for build activity..." : lines.joined(separator: "\n").trimmingCharacters(in: .newlines)
    }
}
