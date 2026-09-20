import CryptoKit
import Foundation
import Testing

@testable import LazyXcodeCore

@Test func cloudTokenUsesES256AndShortLivedClaims() throws {
    let key = P256.Signing.PrivateKey()
    let credentials = CloudCredentials(issuer: "issuer", keyID: "key-id", privateKey: key)
    let now = Date(timeIntervalSince1970: 1_800_000_000)
    let token = try credentials.token(now: now)
    let parts = token.split(separator: ".")
    func decode(_ part: Substring) throws -> Data {
        var base64 = part.replacingOccurrences(of: "-", with: "+").replacingOccurrences(of: "_", with: "/")
        while base64.count % 4 != 0 { base64 += "=" }
        return try #require(Data(base64Encoded: base64))
    }
    let header = try JSONValue.decode(decode(parts[0]))
    let claims = try JSONValue.decode(decode(parts[1]))
    #expect(header["alg"].string == "ES256")
    #expect(header["kid"].string == "key-id")
    #expect(claims["aud"].string == "appstoreconnect-v1")
    #expect(claims["exp"].number - claims["iat"].number == 600)
    let signature = try P256.Signing.ECDSASignature(rawRepresentation: decode(parts[2]))
    #expect(key.publicKey.isValidSignature(signature, for: Data((parts[0] + "." + parts[1]).utf8)))
}

@Test func cloudCredentialsAreOptionalAndErrorsDoNotIncludeKeyContents() throws {
    #expect(throws: AppError.self) { try CloudCredentials.load(environment: [:]) }
    let file = FileManager.default.temporaryDirectory.appendingPathComponent(UUID().uuidString)
    defer { try? FileManager.default.removeItem(at: file) }
    try Data("PRIVATE-KEY-SENTINEL".utf8).write(to: file)
    do {
        _ = try CloudCredentials.load(environment: [
            "LAZYXCODE_ASC_ISSUER_ID": "issuer", "LAZYXCODE_ASC_KEY_ID": "key",
            "LAZYXCODE_ASC_PRIVATE_KEY_PATH": file.path,
        ])
        Issue.record("Malformed key accepted")
    } catch { #expect(!error.localizedDescription.contains("PRIVATE-KEY-SENTINEL")) }
}

@Test func cloudRejectsUntrustedPaginationAndResourceIDs() async throws {
    let client = CloudClient(
        credentials: CloudCredentials(issuer: "issuer", keyID: "key", privateKey: P256.Signing.PrivateKey()))
    for target in [
        "https://evil.example/v1/ciProducts", "http://api.appstoreconnect.apple.com/v1/ciProducts",
        "https://user@api.appstoreconnect.apple.com/v1/ciProducts",
    ] {
        do {
            _ = try await client.page(target)
            Issue.record("Untrusted URL accepted")
        } catch { #expect(error.localizedDescription.contains("untrusted")) }
    }
    #expect(throws: AppError.self) { try CloudClient.route("ciProducts", id: "../secret", relationship: "buildRuns") }
}

@Test func cloudRefreshPreservesOlderPagesAndUpdatesIncludedResources() {
    func resource(_ id: String, name: String = "") -> CloudResource {
        CloudResource(
            .object(["id": .string(id), "type": .string("ciBuildRuns"), "attributes": .object(["name": .string(name)])])
        )
    }
    var page = CloudPage(items: [resource("3"), resource("2")], included: [], next: "page2")
    page.merge(
        CloudPage(items: [resource("1")], included: [resource("workflow", name: "Old")], next: "page3"), older: true)
    page.merge(
        CloudPage(items: [resource("4"), resource("3")], included: [resource("workflow", name: "New")], next: "page2"),
        older: false)
    #expect(page.items.map(\.id) == ["4", "3", "2", "1"])
    #expect(page.next == "page3")
    #expect(page.included.map(\.name) == ["New"])
}

@Test func cloudLogRecognitionAndFingerprintTrackChanges() {
    let log = CloudResource(
        .object([
            "id": .string("log"), "attributes": .object(["fileName": .string("BUILD.TXT"), "fileSize": .number(42)]),
        ]))
    #expect(log.isLog)
    #expect(log.logFingerprint == "log:42")
}
