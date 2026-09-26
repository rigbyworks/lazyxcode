import Foundation
import Testing

@testable import LazyXcode

@Test func cloudLogReaderLimitsFilesAndBytes() async throws {
    let root = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    try FileManager.default.createDirectory(at: root, withIntermediateDirectories: true)
    defer { try? FileManager.default.removeItem(at: root) }
    for name in ["one", "two", "zzz"] {
        try Data(name.utf8).write(to: root.appendingPathComponent(name))
    }

    let files = try await CloudLogReader.read(root, maxFiles: 2, maxBytes: 100)
    #expect(files.filesRead == 2)
    #expect(files.bytesRead == 6)
    #expect(files.truncated)
    #expect(!files.text.contains("zzz"))

    let bytes = try await CloudLogReader.read(root, maxFiles: 10, maxBytes: 2)
    #expect(bytes.filesRead == 1)
    #expect(bytes.bytesRead == 2)
    #expect(bytes.truncated)
    #expect(bytes.text.contains("ne"))
}
