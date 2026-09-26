import Foundation
import Testing

@testable import LazyXcodeCore

@Test func repeatedBuildWorkStaysInOneRowPerPhase() {
    var output = BuildOutput()
    for _ in 0..<400 {
        output.append(Data("SwiftCompile App.swift\nLd App\nCompileAssetCatalog Assets\n".utf8))
    }
    let text = output.text()
    #expect(text.contains("BUILD STEPS"))
    #expect(text.components(separatedBy: "Compile sources").count == 2)
    #expect(text.components(separatedBy: "Compile resources").count == 2)
    #expect(!text.contains("→"))
    #expect(!text.contains("limit"))
    #expect(text.split(separator: "\n").count < 15)
}

@Test func compilerCommandLengthDoesNotTruncateConciseOutput() {
    var output = BuildOutput()
    output.append(Data(("    /usr/bin/swiftc " + String(repeating: "-I/path ", count: 2000) + "\n").utf8))
    output.append(Data("/src/App.swift:12:3: warning: unused value\n/src/App.swift:12:3: warning: unused value\n".utf8))
    let text = output.text()
    #expect(text.contains("DIAGNOSTICS (1)"))
    #expect(text.contains("warning: App.swift:12:3 — unused value"))
    #expect(!text.contains("limit"))
    #expect(!text.contains("/src/"))
}

@Test func oldGoProgressMarkersKeepRecordedDurations() {
    var output = BuildOutput()
    output.append(
        Data(
            """
            [lazyxcode:step] start 1000000 4 Compile sources
            [lazyxcode:step] done 12500 4 Compile sources
            [lazyxcode:step] start 1012500 5 Link
            [lazyxcode:step] done 1500 5 Link
            ** BUILD SUCCEEDED **

            """.utf8))
    let text = output.text(now: Date(timeIntervalSince1970: 5000))
    #expect(text.contains("12.5s"))
    #expect(text.contains("1.5s"))
    #expect(!text.contains("[lazyxcode:step]"))
}

@Test func testOutputGroupsCasesAndShowsSourceFailures() {
    var output = BuildOutput()
    output.append(
        Data(
            """
            Test Suite 'AppTests.xctest' started at now
            Test Suite 'AccountTests' started at now
            Test Case '-[AppTests.AccountTests testLogin]' started.
            Test Case '-[AppTests.AccountTests testLogin]' passed (0.250 seconds).
            Test Case '-[AppTests.AccountTests testLogout]' started.
            /src/AccountTests.swift:42:7: error: expected signed out
            Test Case '-[AppTests.AccountTests testLogout]' failed (0.125 seconds).
            Test Suite 'AccountTests' failed at now
            ** TEST FAILED **

            """.utf8))
    let text = output.text()
    #expect(text.contains("TEST SUITES (1)"))
    #expect(text.contains("1 passed, 1 failed"))
    #expect(text.contains("375ms"))
    #expect(text.contains("AccountTests.swift:42:7"))
    #expect(!text.contains("Test Case '-["))
}

@Test func consoleWithoutNewlineIsVisibleAndGoConsoleMarkerWorks() {
    var output = BuildOutput()
    output.append(Data("[lazyxcode] App console\npartial console message".utf8))
    #expect(output.text().contains("APP CONSOLE\npartial console message"))
}

@Test func liveProgressDurationsSurviveLogReplay() throws {
    let fixture = try TemporaryProject()
    defer { fixture.remove() }
    let url = fixture.store.logURL(id: "timings")
    let log = try ActivityLog(url: url)
    log.append(Data("SwiftCompile A.swift\nLd App\nSwiftCompile B.swift\n** BUILD SUCCEEDED **\n".utf8))
    log.finish()
    let recorded = try Data(contentsOf: url)
    #expect(String(decoding: recorded, as: UTF8.self).contains("[lazyxcode:step] done"))
    var restored = BuildOutput()
    restored.append(recorded)
    restored.finish()
    #expect(restored.text() == log.summary().text())
}

@Test func timingAccumulatesRepeatedPhasesAndStopsDuringTests() {
    var output = BuildOutput()
    let start = Date(timeIntervalSince1970: 1000)
    output.append(Data("SwiftCompile A.swift\n".utf8), at: start)
    output.append(Data("Ld App\n".utf8), at: start.addingTimeInterval(3))
    output.append(Data("SwiftCompile B.swift\n".utf8), at: start.addingTimeInterval(5))
    output.append(Data("Test Suite 'Checks' started\n".utf8), at: start.addingTimeInterval(9))
    let text = output.text(now: start.addingTimeInterval(100))
    #expect(text.contains("7.0s"))
    #expect(text.contains("2.0s"))
    #expect(!text.contains("1m"))
}

@Test func resultsAndCoverageAreReadableWithoutJSON() throws {
    let detail = try JSONValue.decode(
        Data(
            #"{"testName":"testLogin()","testResult":"Failed","duration":"0.1s","testRuns":[{"name":"Expected true","sourceLocation":{"filePath":"/src/App.swift","lineNumber":12}}]}"#
                .utf8))
    let text = XcodeClient.testDetailsText(detail)
    #expect(text.contains("Failed  0.1s"))
    #expect(text.contains("Expected true  /src/App.swift:12"))
    #expect(!text.contains("sourceLocation"))
    let change = try JSONValue.decode(
        Data(
            #"{"lineCoverageDelta":{"lineCoverageDelta":0.25},"targetDeltas":[{"name":"App","lineCoverageDelta":{"lineCoverageDelta":0.25}}],"addedFiles":["New.swift"]}"#
                .utf8))
    let comparison = XcodeClient.coverageDifferenceText(change)
    #expect(comparison.contains("+25.00 pp  Overall"))
    #expect(comparison.contains("Added file: New.swift"))
}
