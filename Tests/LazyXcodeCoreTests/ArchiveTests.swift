import Foundation
import Testing
import ZIPFoundation

@testable import LazyXcodeCore

@Test func resultArchiveExtractsOneBundle() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let url = fixture.root.appendingPathComponent("results.zip")
    let archive = try Archive(url: url, accessMode: .create)
    let contents = Data("test result".utf8)
    try archive.addEntry(with: "Tests.xcresult/data", type: .file, uncompressedSize: Int64(contents.count)) {
        position, count in
        contents.subdata(in: Int(position)..<min(contents.count, Int(position) + count))
    }
    let result = try ResultArchive.expand(url)
    #expect(result.lastPathComponent == "Tests.xcresult")
    #expect(try Data(contentsOf: result.appendingPathComponent("data")) == contents)
}

@Test func resultArchiveReusesValidatedExtraction() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let url = fixture.root.appendingPathComponent("results.zip")
    let archive = try Archive(url: url, accessMode: .create)
    let contents = Data("test result".utf8)
    try archive.addEntry(with: "Tests.xcresult/data", type: .file, uncompressedSize: Int64(contents.count)) {
        position, count in
        contents.subdata(in: Int(position)..<min(contents.count, Int(position) + count))
    }
    let first = try ResultArchive.expand(url)
    let firstSize = try #require(FileManager.default.attributesOfItem(atPath: url.path)[.size] as? Int)
    let second = try ResultArchive.expand(url)
    #expect(first == second)
    #expect(try Data(contentsOf: second.appendingPathComponent("data")) == contents)

    try FileManager.default.removeItem(at: url)
    let updated = Data("updated test result contents".utf8)
    let replacement = try Archive(url: url, accessMode: .create)
    try replacement.addEntry(with: "Tests.xcresult/data", type: .file, uncompressedSize: Int64(updated.count)) {
        position, count in
        updated.subdata(in: Int(position)..<min(updated.count, Int(position) + count))
    }
    let updatedSize = try #require(FileManager.default.attributesOfItem(atPath: url.path)[.size] as? Int)
    #expect(updatedSize != firstSize)
    let refreshed = try ResultArchive.expand(url)
    #expect(refreshed == first)
    #expect(try Data(contentsOf: refreshed.appendingPathComponent("data")) == updated)
}

@Test(arguments: ["../escape", "/absolute", "Tests.xcresult/../../escape", "Tests.xcresult\\escape"])
func resultArchiveRejectsUnsafePaths(_ path: String) throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let url = fixture.root.appendingPathComponent("results.zip")
    let archive = try Archive(url: url, accessMode: .create)
    try archive.addEntry(with: path, type: .file, uncompressedSize: Int64(1)) { _, _ in Data([1]) }
    #expect(throws: AppError.self) { try ResultArchive.expand(url) }
}

@Test func resultArchiveRejectsSymlinksAndMultipleBundles() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let url = fixture.root.appendingPathComponent("symlink.zip")
    let archive = try Archive(url: url, accessMode: .create)
    let link = Data("/etc".utf8)
    try archive.addEntry(with: "Tests.xcresult/link", type: .symlink, uncompressedSize: Int64(link.count)) { _, _ in
        link
    }
    #expect(throws: AppError.self) { try ResultArchive.expand(url) }
    let multiple = fixture.root.appendingPathComponent("multiple.zip")
    let other = try Archive(url: multiple, accessMode: .create)
    for name in ["One.xcresult/data", "Two.xcresult/data"] {
        try other.addEntry(with: name, type: .file, uncompressedSize: Int64(1)) { _, _ in Data([1]) }
    }
    #expect(throws: AppError.self) { try ResultArchive.expand(multiple) }
}

@Test func summaryRetainsEarlyDiagnosticsAndBoundsConsole() {
    var summary = BuildOutput()
    summary.append(Data("App.swift:1: warning: retained\n".utf8))
    summary.append(Data(String(repeating: "CompileSwift App.swift\n", count: 20_000).utf8))
    #expect(summary.text().contains("warning: Build — retained"))
    summary.append(Data("[lazyxcode time] running\n".utf8))
    summary.append(Data(String(repeating: "console output\n", count: 2500).utf8))
    #expect(summary.text().contains("APP CONSOLE"))
    #expect(summary.text().contains("Earlier console output omitted"))
    #expect(summary.text().utf8.count < 256 * 1024)
}
