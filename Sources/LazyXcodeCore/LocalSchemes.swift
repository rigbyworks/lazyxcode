import Foundation

extension XcodeClient {
    /// Shared schemes for immediate display while Xcode loads its complete list, including automatic schemes.
    public func localSchemes(_ container: Container) -> [String] {
        var names = Set<String>()
        var visited = Set<String>()
        func read(_ bundle: URL) {
            guard ["xcodeproj", "xcworkspace"].contains(bundle.pathExtension),
                visited.insert(bundle.resolvingSymlinksInPath().path).inserted
            else { return }
            let directory = bundle.appendingPathComponent("xcshareddata/xcschemes")
            let files = try? FileManager.default.contentsOfDirectory(
                at: directory, includingPropertiesForKeys: nil, options: [.skipsHiddenFiles])
            for file in files ?? [] where file.pathExtension == "xcscheme" {
                names.insert(file.deletingPathExtension().lastPathComponent)
            }
            guard bundle.pathExtension == "xcworkspace",
                let data = try? Data(contentsOf: bundle.appendingPathComponent("contents.xcworkspacedata")),
                let document = try? XMLDocument(data: data, options: [.nodeLoadExternalEntitiesNever]),
                let root = document.rootElement()
            else { return }
            let containerRoot = bundle.deletingLastPathComponent()
            func walk(_ element: XMLElement, relativeTo base: URL) {
                var location = base
                if let reference = element.attribute(forName: "location")?.stringValue {
                    let parts = reference.split(separator: ":", maxSplits: 1, omittingEmptySubsequences: false)
                    guard parts.count == 2 else { return }
                    let path = String(parts[1])
                    switch parts[0] {
                    case "group": location = base.appendingPathComponent(path)
                    case "container": location = containerRoot.appendingPathComponent(path)
                    case "self": location = bundle.appendingPathComponent(path)
                    case "absolute": location = URL(fileURLWithPath: path)
                    default: return
                    }
                }
                if element.name == "FileRef" {
                    read(location)
                } else {
                    for child in element.children ?? [] {
                        if let child = child as? XMLElement { walk(child, relativeTo: location) }
                    }
                }
            }
            walk(root, relativeTo: containerRoot)
        }
        read(URL(fileURLWithPath: container.path))
        return names.sorted()
    }
}
