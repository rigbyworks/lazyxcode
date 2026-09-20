import Foundation

public struct TestTarget: Sendable {
    public var name: String
    public var isUI: Bool
}

extension XcodeClient {
    public func testTargets(_ container: Container, scheme: String) throws -> [TestTarget] {
        let root = URL(fileURLWithPath: container.path).deletingLastPathComponent()
        let direct = URL(fileURLWithPath: container.path).appendingPathComponent(
            "xcshareddata/xcschemes/\(scheme).xcscheme")
        let files = FileManager.default.enumerator(
            at: root, includingPropertiesForKeys: nil, options: [.skipsHiddenFiles])
        var schemes: [URL] = []
        var projects: [URL] = []
        while let url = files?.nextObject() as? URL {
            if ["DerivedData", "SourcePackages", "Pods"].contains(url.lastPathComponent) {
                files?.skipDescendants()
                continue
            }
            if url.lastPathComponent == "\(scheme).xcscheme" { schemes.append(url) }
            if url.lastPathComponent == "project.pbxproj" { projects.append(url) }
        }
        guard
            let url = FileManager.default.fileExists(atPath: direct.path)
                ? direct : schemes.sorted(by: { $0.path < $1.path }).first
        else {
            throw AppError("Test metadata unavailable for scheme \(scheme)")
        }
        let document = try XMLDocument(contentsOf: url)
        var references: [(String, String)] = []
        for node in try document.nodes(forXPath: "//TestAction/Testables/TestableReference") {
            guard let element = node as? XMLElement,
                element.attribute(forName: "skipped")?.stringValue?.uppercased() != "YES",
                let reference = element.elements(forName: "BuildableReference").first
            else { continue }
            references.append(
                (
                    reference.attribute(forName: "BlueprintIdentifier")?.stringValue ?? "",
                    reference.attribute(forName: "BlueprintName")?.stringValue ?? ""
                ))
        }
        for node in try document.nodes(forXPath: "//TestPlanReference") {
            guard let reference = (node as? XMLElement)?.attribute(forName: "reference")?.stringValue,
                reference.hasPrefix("container:")
            else { continue }
            let plan = root.appendingPathComponent(String(reference.dropFirst("container:".count)))
            guard let data = try? Data(contentsOf: plan), let value = try? JSONValue.decode(data) else { continue }
            for target in value["testTargets"].array where target["enabled"] != .bool(false) {
                references.append((target["target"]["identifier"].string, target["target"]["name"].string))
            }
        }
        var kinds: [String: Bool] = [:]
        for project in projects {
            if let plist = try? PropertyListSerialization.propertyList(from: Data(contentsOf: project), format: nil)
                as? [String: Any],
                let objects = plist["objects"] as? [String: [String: Any]]
            {
                for (id, object) in objects where object["isa"] as? String == "PBXNativeTarget" {
                    kinds[id] = (object["productType"] as? String) == "com.apple.product-type.bundle.ui-testing"
                }
            }
        }
        var seen = Set<String>()
        return references.compactMap { id, name in
            guard !name.isEmpty, seen.insert(name).inserted else { return nil }
            return TestTarget(name: name, isUI: kinds[id] ?? name.localizedCaseInsensitiveContains("uitest"))
        }.sorted { $0.name < $1.name }
    }

    public static func enumeratedTests(_ data: Data) throws -> [String] {
        let document = try JSONValue.decode(data)
        guard document["errors"].array.isEmpty else { throw AppError(document["errors"].pretty) }
        var tests = Set<String>()
        func walk(_ value: JSONValue) {
            if !value["identifier"].string.isEmpty { tests.insert(value["identifier"].string) }
            for child in value.array { walk(child) }
            for (key, child) in value.object where key != "disabledTests" { walk(child) }
        }
        walk(document["values"])
        return tests.sorted()
    }

    public func testResults(_ path: String) async throws -> [TestCase] {
        let data = try await runner.run(
            "xcrun", ["xcresulttool", "get", "test-results", "tests", "--path", path, "--compact"])
        return try Self.parseTestResults(data)
    }
    public static func parseTestResults(_ data: Data) throws -> [TestCase] {
        let document = try JSONValue.decode(data)
        guard document["testNodes"] != .null else { throw AppError("Result bundle contains no test tree") }
        var tests: [TestCase] = []
        func walk(_ nodes: [JSONValue], target: String) {
            for node in nodes {
                var bundle = target
                if ["Unit test bundle", "UI test bundle"].contains(node["nodeType"].string) {
                    bundle = node["name"].string.replacingOccurrences(of: ".xctest", with: "")
                }
                if node["nodeType"].string == "Test Case" {
                    var identifier = node["nodeIdentifier"].string
                    if !identifier.isEmpty && !bundle.isEmpty && !identifier.hasPrefix(bundle + "/") {
                        identifier = bundle + "/" + identifier
                    }
                    let id =
                        node["nodeIdentifierURL"].string.isEmpty
                        ? node["nodeIdentifier"].string : node["nodeIdentifierURL"].string
                    if !id.isEmpty {
                        tests.append(
                            TestCase(
                                id: id, identifier: identifier,
                                name: identifier.isEmpty ? node["name"].string : identifier,
                                result: node["result"].string, duration: node["duration"].string))
                    }
                } else {
                    walk(node["children"].array, target: bundle)
                }
            }
        }
        walk(document["testNodes"].array, target: "")
        return tests.sorted { $0.name < $1.name }
    }
    public func testDetails(_ path: String, id: String, activities: Bool = false) async throws -> String {
        let data = try await runner.run(
            "xcrun",
            [
                "xcresulttool", "get", "test-results", activities ? "activities" : "test-details",
                "--path", path, "--test-id", id, "--compact",
            ])
        return try Self.testDetailsText(JSONValue.decode(data), activities: activities)
    }
    public func exportAttachments(_ path: String, id: String) async throws -> URL {
        let directory = URL(fileURLWithPath: path).deletingLastPathComponent().appendingPathComponent(
            "attachments-\(UUID().uuidString)")
        try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        do {
            try await runner.run(
                "xcrun",
                [
                    "xcresulttool", "export", "attachments", "--path", path, "--test-id", id, "--output-path",
                    directory.path,
                ])
            return directory
        } catch {
            try? FileManager.default.removeItem(at: directory)
            throw error
        }
    }
    public func coverage(_ path: String) async throws -> JSONValue {
        try JSONValue.decode(await runner.run("xcrun", ["xccov", "view", "--report", "--json", path]))
    }
    public func compareCoverage(_ path: String, baseline: String) async throws -> JSONValue {
        try JSONValue.decode(await runner.run("xcrun", ["xccov", "diff", "--json", baseline, path]))
    }
    public static func coverageText(_ value: JSONValue, depth: Int = 0) -> String {
        let name = value["path"].string.isEmpty ? value["name"].string : value["path"].string
        let title = name.isEmpty ? "Total coverage" : name
        var lines = [
            String(repeating: "  ", count: min(depth, 12))
                + String(
                    format: "%6.2f%%  %@  (%d/%d)",
                    value["lineCoverage"].number * 100, title, Int(value["coveredLines"].number),
                    Int(value["executableLines"].number))
        ]
        for key in ["targets", "files", "functions"] {
            lines += value[key].array.map { coverageText($0, depth: depth + 1) }
        }
        return lines.joined(separator: "\n")
    }
}
