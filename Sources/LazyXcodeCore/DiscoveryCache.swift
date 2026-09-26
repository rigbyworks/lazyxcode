import Foundation

/// Last-known metadata for immediate display while Xcode checks the current project and devices.
public struct DiscoveryCache: Sendable {
    private let root: URL
    private struct Entry<Value: Codable>: Codable {
        let savedAt: Date
        let value: Value
    }

    public init(store: ProjectStore) {
        root = store.cache.appendingPathComponent("discovery")
    }

    public func schemes() -> [String]? { read("schemes") }
    public func destinations(scheme: String) -> [Destination]? {
        read("destinations-" + Store.shortHash(scheme))
    }
    public func saveSchemes(_ schemes: [String]) {
        write(schemes, name: "schemes")
    }
    public func saveDestinations(_ destinations: [Destination], scheme: String) {
        write(destinations, name: "destinations-" + Store.shortHash(scheme))
    }

    private func read<Value: Codable>(_ name: String) -> Value? {
        guard let entry = try? Store.read(Entry<Value>.self, from: root.appendingPathComponent(name + ".json")),
            (0..<86_400).contains(Date().timeIntervalSince(entry.savedAt))
        else { return nil }
        return entry.value
    }
    private func write<Value: Codable>(_ value: Value, name: String) {
        // Caching is optional. A read-only cache must not prevent discovery.
        try? Store.write(Entry(savedAt: Date(), value: value), to: root.appendingPathComponent(name + ".json"))
    }
}
