import Foundation
import Testing

@testable import LazyXcodeCore

@Test func discoveryCachePersistsAndSeparatesProjectsAndSchemes() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let app = Container(kind: .project, name: "App", path: "/App")
    let store = ProjectStore(container: app, stateRoot: root, cacheRoot: root)
    let cache = DiscoveryCache(store: store)
    #expect(cache.schemes() == nil)
    cache.saveSchemes(["App", "Tests"])
    cache.saveDestinations([Destination(id: "mac", name: "My Mac")], scheme: "App")
    let restored = DiscoveryCache(store: store)
    #expect(restored.schemes() == ["App", "Tests"])
    #expect(restored.destinations(scheme: "App")?.map(\.id) == ["mac"])
    #expect(restored.destinations(scheme: "Tests") == nil)
    let other = Container(kind: .workspace, name: "App", path: "/Other")
    #expect(DiscoveryCache(store: ProjectStore(container: other, cacheRoot: root)).schemes() == nil)
    cache.saveSchemes([])
    #expect(restored.schemes() == [])
    try store.clearDerivedData()
    #expect(restored.destinations(scheme: "App")?.count == 1)
}

@Test func discoveryCacheIgnoresExpiredOrCorruptEntries() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let store = ProjectStore(container: Container(kind: .project, name: "App", path: "/App"), cacheRoot: root)
    let cache = DiscoveryCache(store: store)
    cache.saveSchemes(["App"])
    let url = store.cache.appendingPathComponent("discovery/schemes.json")
    try Data(#"{"savedAt":"2000-01-01T00:00:00Z","value":["Old"]}"#.utf8).write(to: url)
    #expect(cache.schemes() == nil)
    try Data("broken".utf8).write(to: url)
    #expect(cache.schemes() == nil)
    cache.saveSchemes(["Recovered"])
    #expect(cache.schemes() == ["Recovered"])
}
