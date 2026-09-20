import Testing

@testable import LazyXcode

@Test func versionIsPresent() { #expect(!CLI.version.isEmpty) }
