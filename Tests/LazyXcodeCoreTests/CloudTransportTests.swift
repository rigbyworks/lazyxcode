import CryptoKit
import Foundation
import Synchronization
import Testing

@testable import LazyXcodeCore

private final class CloudProtocol: URLProtocol, @unchecked Sendable {
    struct State {
        var requests: [URLRequest] = []
        var forbidden = false
        var oversized = false
    }
    static let state = Mutex(State())
    override class func canInit(with request: URLRequest) -> Bool { true }
    override class func canonicalRequest(for request: URLRequest) -> URLRequest { request }
    override func startLoading() {
        let forbidden = Self.state.withLock { state in
            state.requests.append(request)
            return state.forbidden
        }
        let body: String
        if request.url?.query == "page=2" {
            body = #"{"data":[{"type":"ciProducts","id":"two","attributes":{"name":"Second"}}],"links":{}}"#
        } else {
            body =
                #"{"data":[{"type":"ciProducts","id":"one","attributes":{"name":"First"}}],"links":{"next":"https://api.appstoreconnect.apple.com/v1/ciProducts?page=2"}}"#
        }
        let oversized = Self.state.withLock { $0.oversized }
        let response = HTTPURLResponse(
            url: request.url!, statusCode: forbidden ? 403 : 200, httpVersion: "HTTP/1.1",
            headerFields: oversized
                ? ["Content-Type": "application/json", "Content-Length": "40000000"]
                : ["Content-Type": "application/json"])!
        client?.urlProtocol(self, didReceive: response, cacheStoragePolicy: .notAllowed)
        client?.urlProtocol(self, didLoad: Data(body.utf8))
        client?.urlProtocolDidFinishLoading(self)
    }
    override func stopLoading() {}
}

@Suite(.serialized)
struct CloudTransportTests {
    private func client() -> CloudClient {
        let configuration = URLSessionConfiguration.ephemeral
        configuration.protocolClasses = [CloudProtocol.self]
        return CloudClient(
            credentials: CloudCredentials(issuer: "fixture", keyID: "fixture", privateKey: P256.Signing.PrivateKey()),
            apiSession: URLSession(configuration: configuration))
    }
    @Test func productsFollowPaginationUsingOnlyAuthenticatedGETs() async throws {
        CloudProtocol.state.withLock { $0 = CloudProtocol.State() }
        let products = try await client().products()
        #expect(products.map(\.name) == ["First", "Second"])
        let requests = CloudProtocol.state.withLock { $0.requests }
        #expect(requests.count == 2)
        #expect(requests.allSatisfy { $0.httpMethod == "GET" })
        #expect(requests.allSatisfy { $0.value(forHTTPHeaderField: "Authorization")?.hasPrefix("Bearer ") == true })
    }
    @Test func forbiddenResponsesDoNotRetry() async throws {
        CloudProtocol.state.withLock { $0 = CloudProtocol.State(forbidden: true) }
        do {
            _ = try await client().products()
            Issue.record("Expected forbidden response")
        } catch { #expect(error.localizedDescription.contains("403")) }
        #expect(CloudProtocol.state.withLock { $0.requests.count } == 1)
    }
    @Test func oversizedAPIResponsesAreRejectedWithoutRetry() async throws {
        CloudProtocol.state.withLock { $0 = CloudProtocol.State(oversized: true) }
        do {
            _ = try await client().products()
            Issue.record("Oversized response accepted")
        } catch { #expect(error.localizedDescription.contains("32 MiB")) }
        #expect(CloudProtocol.state.withLock { $0.requests.count } == 1)
    }

}
