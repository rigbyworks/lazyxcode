import CryptoKit
import Foundation

public enum Store {
    public static func shortHash(_ value: String) -> String {
        SHA256.hash(data: Data(value.utf8)).prefix(8).map { String(format: "%02x", $0) }.joined()
    }
    public static func root(
        _ variable: String, fallback: String, environment: [String: String] = ProcessInfo.processInfo.environment
    ) -> URL {
        let path =
            environment[variable].flatMap { $0.isEmpty ? nil : $0 }
            ?? FileManager.default.homeDirectoryForCurrentUser.appendingPathComponent(fallback).path
        return URL(fileURLWithPath: path).appendingPathComponent("lazyxcode")
    }
    public static var stateRoot: URL { root("XDG_STATE_HOME", fallback: ".local/state") }
    public static var cacheRoot: URL { root("XDG_CACHE_HOME", fallback: ".cache") }
    public static func write<T: Encodable>(_ value: T, to url: URL) throws {
        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.dateEncodingStrategy = .iso8601
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        try encoder.encode(value).write(to: url, options: .atomic)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: url.path)
    }
    public static func read<T: Decodable>(_ type: T.Type, from url: URL) throws -> T {
        let decoder = JSONDecoder()
        decoder.dateDecodingStrategy = .custom { decoder in
            let value = try decoder.singleValueContainer().decode(String.self)
            let formatter = ISO8601DateFormatter()
            formatter.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
            if let date = formatter.date(from: value) { return date }
            formatter.formatOptions = [.withInternetDateTime]
            guard let date = formatter.date(from: value) else { throw AppError("Invalid history date") }
            return date
        }
        return try decoder.decode(type, from: Data(contentsOf: url))
    }
    public static func safeComponent(_ value: String) throws -> String {
        let base = value.replacingOccurrences(of: "\\", with: "/").components(separatedBy: "/").last ?? ""
        let allowed = Set("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-_")
        let result = String(String(base.map { allowed.contains($0) ? $0 : "_" }).drop(while: { $0 == "." }).prefix(128))
        guard !result.isEmpty else { throw AppError("Invalid path component") }
        return result
    }
    public static func contains(_ url: URL, in root: URL) -> Bool {
        url.resolvingSymlinksInPath().standardizedFileURL.path.hasPrefix(
            root.resolvingSymlinksInPath().standardizedFileURL.path + "/")
    }
}

public struct Preferences: Codable, Sendable {
    public var version = 2
    public var containers: [String: String] = [:]
    public var schemes: [String: String] = [:]
    public var simulators: [String: String] = [:]
    public var cloudProducts: [String: String] = [:]
    public var cloudWorkflows: [String: String] = [:]
    public var coverageDisabled: [String: Bool] = [:]
    public init() {}
    enum CodingKeys: String, CodingKey {
        case version, containers, schemes, simulators, cloudProducts, cloudWorkflows, coverageDisabled
    }
    public init(from decoder: Decoder) throws {
        let values = try decoder.container(keyedBy: CodingKeys.self)
        containers = try values.decodeIfPresent([String: String].self, forKey: .containers) ?? [:]
        schemes = try values.decodeIfPresent([String: String].self, forKey: .schemes) ?? [:]
        simulators = try values.decodeIfPresent([String: String].self, forKey: .simulators) ?? [:]
        cloudProducts = try values.decodeIfPresent([String: String].self, forKey: .cloudProducts) ?? [:]
        cloudWorkflows = try values.decodeIfPresent([String: String].self, forKey: .cloudWorkflows) ?? [:]
        coverageDisabled = try values.decodeIfPresent([String: Bool].self, forKey: .coverageDisabled) ?? [:]
    }
    public static func load(root: URL = Store.stateRoot) throws -> Preferences {
        let url = root.appendingPathComponent("preferences.json")
        guard FileManager.default.fileExists(atPath: url.path) else { return Preferences() }
        return try Store.read(Preferences.self, from: url)
    }
    public func save(root: URL = Store.stateRoot) throws {
        try Store.write(self, to: root.appendingPathComponent("preferences.json"))
    }
}

public struct ProjectStore: Sendable {
    public let state: URL
    public let cache: URL
    struct History: Codable {
        var version = 1
        var builds: [BuildRecord]
    }
    public init(container: Container, stateRoot: URL = Store.stateRoot, cacheRoot: URL = Store.cacheRoot) {
        let key = Store.shortHash(container.kind.rawValue + "\0" + container.path)
        state = stateRoot.appendingPathComponent("projects/\(key)")
        cache = cacheRoot.appendingPathComponent("projects/\(key)")
    }
    public func derivedData(scheme: String, destination: String) -> URL {
        cache.appendingPathComponent("derived-data/\(Store.shortHash(scheme + "\0" + destination))")
    }
    public func resultDirectory(id: String) throws -> URL {
        guard !id.isEmpty, id == (try Store.safeComponent(id)) else { throw AppError("Invalid activity ID") }
        let url = state.appendingPathComponent("results/\(id)")
        try FileManager.default.createDirectory(
            at: url, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        return url
    }
    public func logURL(id: String) -> URL { state.appendingPathComponent("logs/\(id).log") }
    public func load() throws -> [BuildRecord] {
        let url = state.appendingPathComponent("builds.json")
        guard FileManager.default.fileExists(atPath: url.path) else { return [] }
        var records = try Store.read(History.self, from: url).builds
        for index in records.indices where records[index].phase.active {
            records[index].phase = .cancelled
            records[index].finishedAt = Date()
            records[index].error = "lazyxcode exited before the activity completed"
        }
        try save(records)
        return records
    }
    public func save(_ records: [BuildRecord]) throws {
        let sorted = records.sorted { $0.startedAt > $1.startedAt }
        let kept = sorted.enumerated().filter { $0.offset < 100 || $0.element.phase.active }.map(\.element)
        try Store.write(History(builds: kept), to: state.appendingPathComponent("builds.json"))
        for record in sorted where !kept.contains(where: { $0.id == record.id }) {
            let log = URL(fileURLWithPath: record.logPath)
            if Store.contains(log, in: state.appendingPathComponent("logs")) {
                try? FileManager.default.removeItem(at: log)
            }
            if record.id == (try? Store.safeComponent(record.id)) {
                try? FileManager.default.removeItem(at: state.appendingPathComponent("results/\(record.id)"))
            }
        }
    }
    public func logPage(_ record: BuildRecord, end: UInt64? = nil, size: Int = 64 * 1024) throws -> (
        text: String, start: UInt64, end: UInt64
    ) {
        let url = URL(fileURLWithPath: record.logPath)
        guard Store.contains(url, in: state.appendingPathComponent("logs")) else {
            throw AppError("Log is outside project state")
        }
        let file = try FileHandle(forReadingFrom: url)
        defer { try? file.close() }
        let length = try file.seekToEnd()
        let stop = min(end ?? length, length)
        let start = stop > UInt64(max(0, size)) ? stop - UInt64(max(0, size)) : 0
        try file.seek(toOffset: start)
        let data = try file.read(upToCount: Int(stop - start)) ?? Data()
        return (String(decoding: data, as: UTF8.self), start, stop)
    }
    public func clearDerivedData() throws {
        let url = cache.appendingPathComponent("derived-data")
        guard Store.contains(url, in: cache) else { throw AppError("Unsafe cache path") }
        if FileManager.default.fileExists(atPath: url.path) { try FileManager.default.removeItem(at: url) }
    }
    public func artifactURL(run: String, artifact: String, name: String) throws -> URL {
        guard !artifact.isEmpty else { throw AppError("Missing artifact ID") }
        return cache.appendingPathComponent(
            "cloud/\(try Store.safeComponent(run))/artifacts/\(Store.shortHash(artifact))/\(try Store.safeComponent(name))"
        )
    }
}
