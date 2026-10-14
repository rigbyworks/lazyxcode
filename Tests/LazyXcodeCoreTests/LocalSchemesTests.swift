import Foundation
import Testing

@testable import LazyXcodeCore

private func saveSchemes(_ names: [String], in bundle: URL) throws {
    let directory = bundle.appendingPathComponent("xcshareddata/xcschemes")
    try FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
    for name in names {
        try Data("<Scheme version=\"1.3\"/>".utf8).write(to: directory.appendingPathComponent(name + ".xcscheme"))
    }
}

@Test func localSchemesFollowWorkspaceReferencesAndGroupsWithoutScanningUnrelatedProjects() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let workspace = root.appendingPathComponent("App.xcworkspace")
    let nested = root.appendingPathComponent("Nested.xcworkspace")
    let external = root.appendingPathComponent("External/Library.xcodeproj")
    try saveSchemes(["Workspace"], in: workspace)
    try saveSchemes(["App", "Shared"], in: root.appendingPathComponent("Projects/App.xcodeproj"))
    try saveSchemes(["Shared", "Library"], in: external)
    try saveSchemes(["Unrelated"], in: root.appendingPathComponent("Unrelated.xcodeproj"))
    try FileManager.default.createDirectory(at: nested, withIntermediateDirectories: true)
    try Data(
        """
        <Workspace>
          <Group location="group:Projects">
            <FileRef location="group:App.xcodeproj"/>
          </Group>
          <FileRef location="absolute:\(external.path)"/>
          <FileRef location="container:Nested.xcworkspace"/>
          <FileRef location="group:Missing.xcodeproj"/>
        </Workspace>
        """.utf8
    ).write(to: workspace.appendingPathComponent("contents.xcworkspacedata"))
    // A nested workspace can refer back to its parent. Read each bundle only once.
    try Data("<Workspace><FileRef location=\"group:App.xcworkspace\"/></Workspace>".utf8).write(
        to: nested.appendingPathComponent("contents.xcworkspacedata"))
    let container = Container(kind: .workspace, name: "App", path: workspace.path)
    #expect(XcodeClient().localSchemes(container) == ["App", "Library", "Shared", "Workspace"])
}

@Test func localSchemesTolerateMissingAndMalformedMetadata() throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: root) }
    let workspace = root.appendingPathComponent("App.xcworkspace")
    let container = Container(kind: .workspace, name: "App", path: workspace.path)
    #expect(XcodeClient().localSchemes(container).isEmpty)
    try saveSchemes(["App"], in: workspace)
    try Data("broken".utf8).write(to: workspace.appendingPathComponent("contents.xcworkspacedata"))
    #expect(XcodeClient().localSchemes(container) == ["App"])
}
