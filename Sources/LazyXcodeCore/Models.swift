import Foundation

public struct AppError: LocalizedError, Sendable {
    public let message: String
    public init(_ message: String) { self.message = message }
    public var errorDescription: String? { message }
}

public struct Container: Codable, Hashable, Identifiable, Sendable {
    public enum Kind: String, Codable, Sendable { case workspace, project }
    public var kind: Kind
    public var name: String
    public var path: String
    public var id: String { path }
    public var arguments: [String] { [kind == .workspace ? "-workspace" : "-project", path] }
    public init(kind: Kind, name: String, path: String) {
        self.kind = kind
        self.name = name
        self.path = path
    }
    public static func discover(in directory: URL) throws -> [Container] {
        try FileManager.default.contentsOfDirectory(
            at: directory.standardizedFileURL,
            includingPropertiesForKeys: [.isDirectoryKey]
        ).compactMap { url in
            guard try url.resourceValues(forKeys: [.isDirectoryKey]).isDirectory == true else { return nil }
            let kind: Kind
            switch url.pathExtension {
            case "xcworkspace": kind = .workspace
            case "xcodeproj": kind = .project
            default: return nil
            }
            return Container(kind: kind, name: url.lastPathComponent, path: url.path)
        }.sorted {
            $0.kind == $1.kind ? $0.name.localizedStandardCompare($1.name) == .orderedAscending : $0.kind == .workspace
        }
    }
}

public struct Destination: Codable, Hashable, Identifiable, Sendable {
    public var id: String
    public var name: String
    public var os = ""
    public var platform = ""
    public var state = ""
    public var deviceType: String? = nil
    public var physical: Bool? = nil
    public var isMac: Bool { platform == "macOS" }
    public var isSimulator: Bool { !isMac && physical != true }
    public var label: String { name + (os.isEmpty ? "" : " (\(os))") }
    public var kindLabel: String { isMac ? "Mac" : isSimulator ? "Simulator" : "Device" }
    enum CodingKeys: String, CodingKey {
        case id, name, platform, state, deviceType, physical
        case os = "os"
    }
    public init(
        id: String, name: String, os: String = "", platform: String = "", state: String = "", physical: Bool = false
    ) {
        self.id = id
        self.name = name
        self.os = os
        self.platform = platform
        self.state = state
        self.physical = physical
    }
}

public enum Phase: String, Codable, Sendable {
    case queued, building, testing, booting, installing, launching, running, succeeded, cancelled
    case buildFailed = "build_failed"
    case testFailed = "test_failed"
    case runFailed = "run_failed"
    public var active: Bool {
        switch self {
        case .queued, .building, .testing, .booting, .installing, .launching, .running: true
        default: false
        }
    }
}

public enum Operation: String, Codable, Sendable {
    case build, run, test
    case discoverTests = "discover_tests"
}

public struct BuildRecord: Codable, Identifiable, Sendable {
    public var id: String
    public var container: Container
    public var scheme: String
    // Retain the existing history key for all destination types.
    public var simulator: Destination
    public var phase: Phase = .queued
    public var operation: Operation? = .build
    public var testScope: String? = nil
    public var resultBundlePath: String? = nil
    public var enumerationPath: String? = nil
    public var coverage: Bool? = nil
    public var testTargets: [String]? = nil
    public var startedAt: Date = Date()
    public var finishedAt: Date? = nil
    public var error: String? = nil
    public var derivedDataKey: String
    public var logPath: String
    public init(
        id: String = UUID().uuidString, container: Container, scheme: String,
        destination: Destination, operation: Operation, derivedData: String, logPath: String
    ) {
        self.id = id
        self.container = container
        self.scheme = scheme
        self.simulator = destination
        self.operation = operation
        self.derivedDataKey = derivedData
        self.logPath = logPath
    }
    public var duration: String {
        let seconds = max(0, Int((finishedAt ?? Date()).timeIntervalSince(startedAt)))
        return seconds < 60 ? "\(seconds)s" : "\(seconds / 60)m \(seconds % 60)s"
    }
}

public struct TestCase: Identifiable, Hashable, Sendable {
    public var id: String
    public var identifier: String
    public var name: String
    public var result: String
    public var duration: String
    public var failed: Bool { result.localizedCaseInsensitiveContains("fail") }
}

public struct Product: Sendable {
    public var appPath: String
    public var bundleID: String
}
