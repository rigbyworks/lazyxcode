import Foundation

extension XcodeClient {
    public static func testDetailsText(_ value: JSONValue, activities: Bool = false) -> String {
        var lines: [String] = []
        func walk(_ nodes: [JSONValue], depth: Int, activities: Bool) {
            for node in nodes {
                let indent = String(repeating: "  ", count: min(depth, 12))
                if activities {
                    lines.append(
                        indent + node["title"].string + (node["isAssociatedWithFailure"].bool ? " [failure]" : ""))
                    lines += node["attachments"].array.map { indent + "  Attachment: " + $0["name"].string }
                    walk(node["childActivities"].array, depth: depth + 1, activities: true)
                } else {
                    var line = indent + node["name"].string
                    if !node["result"].string.isEmpty { line += "  [\(node["result"].string)]" }
                    if !node["details"].string.isEmpty && node["details"] != node["name"] {
                        line += "  " + node["details"].string
                    }
                    let location = node["sourceLocation"]
                    if !location["filePath"].string.isEmpty {
                        line += "  \(location["filePath"].string):\(Int(location["lineNumber"].number))"
                    }
                    lines.append(line)
                    walk(node["children"].array, depth: depth + 1, activities: false)
                }
            }
        }
        if activities {
            let runs = value["testRuns"].array.isEmpty ? [value["testRuns"]] : value["testRuns"].array
            for run in runs {
                lines += [
                    run["device"]["deviceName"].string + "  "
                        + run["testPlanConfiguration"]["configurationName"].string, "",
                ]
                walk(run["activities"].array, depth: 0, activities: true)
            }
        } else {
            lines = [value["testName"].string, value["testResult"].string + "  " + value["duration"].string, ""]
            if !value["testDescription"].string.isEmpty { lines.append(value["testDescription"].string) }
            walk(value["testRuns"].array, depth: 0, activities: false)
        }
        return lines.joined(separator: "\n")
    }
    public static func coverageDifferenceText(_ value: JSONValue, depth: Int = 0) -> String {
        let indent = String(repeating: "  ", count: min(depth, 12))
        let name =
            depth == 0
            ? "Overall"
            : value["documentLocation"].string.isEmpty ? value["name"].string : value["documentLocation"].string
        let delta = value["lineCoverageDelta"]
        var lines = [
            indent
                + String(
                    format: "%+.2f pp  %@  (%+d covered, %+d executable lines)",
                    delta["lineCoverageDelta"].number * 100, name, Int(delta["coveredLinesDelta"].number),
                    Int(delta["executableLinesDelta"].number))
        ]
        for (key, label) in [
            ("addedTargets", "Added target"), ("removedTargets", "Removed target"), ("addedFiles", "Added file"),
            ("removedFiles", "Removed file"), ("addedFunctions", "Added function"),
            ("removedFunctions", "Removed function"),
        ] {
            for entry in value[key].array {
                let name =
                    [entry.string, entry["documentLocation"].string, entry["path"].string, entry["name"].string].first {
                        !$0.isEmpty
                    } ?? ""
                lines.append(indent + "  " + label + ": " + name)
            }
        }
        for key in ["targetDeltas", "fileDeltas", "functionDeltas"] {
            lines += value[key].array.map { coverageDifferenceText($0, depth: depth + 1) }
        }
        return lines.joined(separator: "\n")
    }
}
