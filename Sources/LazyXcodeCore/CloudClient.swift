import CryptoKit
import Foundation

public struct CloudCredentials: Sendable {
    public var issuer: String
    public var keyID: String
    public var privateKey: P256.Signing.PrivateKey
    public var warning: String?
    public static func load(environment: [String: String] = ProcessInfo.processInfo.environment) throws
        -> CloudCredentials
    {
        let names = ["LAZYXCODE_ASC_ISSUER_ID", "LAZYXCODE_ASC_KEY_ID", "LAZYXCODE_ASC_PRIVATE_KEY_PATH"]
        let values = names.map { environment[$0]?.trimmingCharacters(in: .whitespacesAndNewlines) ?? "" }
        guard values.allSatisfy({ !$0.isEmpty }) else {
            throw AppError("Set \(names.joined(separator: ", ")) to browse Xcode Cloud. Local builds need no API key.")
        }
        guard let data = FileManager.default.contents(atPath: values[2]), let pem = String(data: data, encoding: .utf8)
        else {
            throw AppError("App Store Connect private key is unreadable")
        }
        guard let key = try? P256.Signing.PrivateKey(pemRepresentation: pem) else {
            throw AppError("App Store Connect private key must be a PEM encoded P-256 key")
        }
        let attributes = try FileManager.default.attributesOfItem(atPath: values[2])
        let mode = (attributes[.posixPermissions] as? NSNumber)?.intValue ?? 0
        return CloudCredentials(
            issuer: values[0], keyID: values[1], privateKey: key,
            warning: mode & 0o077 != 0 ? "Private key is readable by other users. Set its permissions to 600." : nil)
    }
    public func token(now: Date = Date()) throws -> String {
        func encoded(_ value: JSONValue) throws -> String { try JSONEncoder().encode(value).base64URL }
        let header = try encoded(.object(["alg": .string("ES256"), "kid": .string(keyID), "typ": .string("JWT")]))
        let claims = try encoded(
            .object([
                "iss": .string(issuer), "iat": .number(floor(now.timeIntervalSince1970)),
                "exp": .number(floor(now.timeIntervalSince1970) + 600), "aud": .string("appstoreconnect-v1"),
            ]))
        let message = header + "." + claims
        return try message + "." + privateKey.signature(for: Data(message.utf8)).rawRepresentation.base64URL
    }
}

extension Data {
    fileprivate var base64URL: String {
        base64EncodedString().replacingOccurrences(of: "+", with: "-").replacingOccurrences(of: "/", with: "_")
            .replacingOccurrences(of: "=", with: "")
    }
}

public struct CloudResource: Identifiable, Sendable, Equatable {
    public var value: JSONValue
    public var id: String { value["id"].string }
    public var attributes: JSONValue { value["attributes"] }
    public var name: String {
        let name = attributes["name"].string
        return name.isEmpty ? attributes["fileName"].string : name
    }
    public var active: Bool { ["PENDING", "RUNNING"].contains(attributes["executionProgress"].string) }
    public var status: String {
        let complete = attributes["completionStatus"].string
        return complete.isEmpty ? attributes["executionProgress"].string : complete
    }
    public init(_ value: JSONValue) { self.value = value }
}

public struct CloudPage: Sendable {
    public var items: [CloudResource]
    public var included: [CloudResource]
    public var next: String
    public var hasOlderPages = false
    public init(items: [CloudResource], included: [CloudResource], next: String) {
        self.items = items
        self.included = included
        self.next = next
    }
    public func related(_ resource: CloudResource, _ relationship: String) -> CloudResource? {
        let reference = resource.value["relationships"][relationship]["data"]
        return included.first { $0.id == reference["id"].string && $0.value["type"] == reference["type"] }
    }
}

public struct CloudDetails: Sendable {
    public var text: String
    public var artifacts: [CloudResource]
}

private final class CloudRedirectPolicy: NSObject, URLSessionTaskDelegate, Sendable {
    let api: Bool
    init(api: Bool) { self.api = api }
    func urlSession(
        _ session: URLSession, task: URLSessionTask, willPerformHTTPRedirection response: HTTPURLResponse,
        newRequest request: URLRequest, completionHandler: @escaping (URLRequest?) -> Void
    ) {
        guard let url = request.url, url.scheme == "https", url.user == nil, url.password == nil,
            !api || CloudClient.isAPIURL(url)
        else {
            completionHandler(nil)
            return
        }
        completionHandler(request)
    }
}

public actor CloudClient {
    public let credentials: CloudCredentials
    private let apiSession: URLSession
    private let downloadSession: URLSession
    public init(credentials: CloudCredentials, apiSession: URLSession? = nil, downloadSession: URLSession? = nil) {
        self.credentials = credentials
        let configuration = URLSessionConfiguration.ephemeral
        configuration.timeoutIntervalForRequest = 60
        self.apiSession =
            apiSession
            ?? URLSession(configuration: configuration, delegate: CloudRedirectPolicy(api: true), delegateQueue: nil)
        self.downloadSession =
            downloadSession
            ?? URLSession(configuration: configuration, delegate: CloudRedirectPolicy(api: false), delegateQueue: nil)
    }
    public static func isAPIURL(_ url: URL) -> Bool {
        url.scheme == "https" && url.host == "api.appstoreconnect.apple.com" && (url.port == nil || url.port == 443)
            && url.user == nil && url.password == nil && url.path.hasPrefix("/v1/")
    }
    public static func route(_ type: String, id: String, relationship: String) throws -> String {
        guard !id.isEmpty,
            id.unicodeScalars.allSatisfy({
                CharacterSet.alphanumerics.union(CharacterSet(charactersIn: "-_")).contains($0)
            })
        else { throw AppError("Invalid Cloud resource ID") }
        return "/v1/\(type)/\(id)/\(relationship)"
    }
    public func page(_ path: String) async throws -> CloudPage {
        guard
            let url = URL(string: path, relativeTo: URL(string: "https://api.appstoreconnect.apple.com"))?.absoluteURL,
            Self.isAPIURL(url)
        else { throw AppError("Refusing an untrusted App Store Connect URL") }
        for attempt in 0..<4 {
            try Task.checkCancellation()
            var request = URLRequest(url: url)
            request.httpMethod = "GET"
            request.setValue("Bearer \(try credentials.token())", forHTTPHeaderField: "Authorization")
            request.setValue("application/json", forHTTPHeaderField: "Accept")
            let data: Data
            let response: URLResponse
            do {
                let (bytes, receivedResponse) = try await apiSession.bytes(for: request)
                response = receivedResponse
                let limit = 32 * 1024 * 1024
                guard response.expectedContentLength <= limit else {
                    bytes.task.cancel()
                    throw AppError("App Store Connect response exceeded 32 MiB")
                }
                var body = Data()
                for try await byte in bytes {
                    guard body.count < limit else {
                        bytes.task.cancel()
                        throw AppError("App Store Connect response exceeded 32 MiB")
                    }
                    body.append(byte)
                }
                data = body
            } catch let error as AppError { throw error } catch {
                try Task.checkCancellation()
                if attempt < 3 {
                    try await Task.sleep(for: .seconds(1 << attempt))
                    continue
                }
                throw AppError("App Store Connect connection failed")
            }
            guard let http = response as? HTTPURLResponse else { throw AppError("Invalid App Store Connect response") }
            if (http.statusCode == 429 || http.statusCode >= 500) && attempt < 3 {
                let retry = Double(http.value(forHTTPHeaderField: "Retry-After") ?? "") ?? Double(1 << attempt)
                try await Task.sleep(for: .seconds(min(60, max(1, retry))))
                continue
            }
            guard (200..<300).contains(http.statusCode) else {
                let message =
                    http.statusCode == 401
                    ? "Check the issuer, key ID, and private key."
                    : http.statusCode == 403 ? "The API key cannot read this Xcode Cloud resource." : "Retry later."
                throw AppError("App Store Connect HTTP \(http.statusCode). \(message)")
            }
            let document = try JSONValue.decode(data)
            let resources =
                document["data"].array.isEmpty && document["data"].object["id"] != nil
                ? [document["data"]] : document["data"].array
            return CloudPage(
                items: resources.map(CloudResource.init), included: document["included"].array.map(CloudResource.init),
                next: document["links"]["next"].string)
        }
        throw AppError("App Store Connect retry limit reached")
    }
    public func all(_ path: String) async throws -> [CloudResource] {
        var next = path
        var visited = Set<String>()
        var items: [CloudResource] = []
        while !next.isEmpty {
            guard visited.insert(next).inserted, visited.count <= 1000 else {
                throw AppError("Invalid Cloud pagination")
            }
            let page = try await page(next)
            items += page.items
            next = page.next
        }
        return items
    }
    public func products() async throws -> [CloudResource] { try await all("/v1/ciProducts?limit=200") }
    public func workflows(_ product: String) async throws -> [CloudResource] {
        try await all(Self.route("ciProducts", id: product, relationship: "workflows") + "?limit=200")
    }
    public func runs(product: String, workflow: String, cursor: String = "") async throws -> CloudPage {
        let route = try Self.route(
            workflow.isEmpty ? "ciProducts" : "ciWorkflows", id: workflow.isEmpty ? product : workflow,
            relationship: "buildRuns")
        return try await page(
            cursor.isEmpty
                ? route + "?sort=-number&limit=25&include=workflow,sourceBranchOrTag,destinationBranch" : cursor)
    }
    public func details(_ run: CloudResource, page: CloudPage) async throws -> CloudDetails {
        let actions = try await all(Self.route("ciBuildRuns", id: run.id, relationship: "actions") + "?limit=200")
        typealias Detail = (CloudResource, [CloudResource], [CloudResource], [CloudResource])
        var collected: [Detail] = []
        for start in stride(from: 0, to: actions.count, by: 4) {
            let batch = Array(actions[start..<min(start + 4, actions.count)])
            let details = try await withThrowingTaskGroup(of: Detail.self) { group in
                for action in batch {
                    group.addTask {
                        let issues = try await self.all(
                            Self.route("ciBuildActions", id: action.id, relationship: "issues") + "?limit=200")
                        let tests =
                            action.attributes["actionType"].string == "TEST"
                            ? try await self.all(
                                Self.route("ciBuildActions", id: action.id, relationship: "testResults") + "?limit=200")
                            : []
                        let artifacts = try await self.all(
                            Self.route("ciBuildActions", id: action.id, relationship: "artifacts") + "?limit=200")
                        return (action, issues, tests, artifacts)
                    }
                }
                var result: [Detail] = []
                for try await detail in group { result.append(detail) }
                return result.sorted { lhs, rhs in
                    (batch.firstIndex(where: { $0.id == lhs.0.id }) ?? 0)
                        < (batch.firstIndex(where: { $0.id == rhs.0.id }) ?? 0)
                }
            }
            collected += details
        }
        return CloudDetails.format(run: run, page: page, actions: collected)
    }
    public func download(_ artifact: CloudResource, to destination: URL) async throws -> URL {
        guard let url = URL(string: artifact.attributes["downloadUrl"].string), url.scheme == "https",
            url.host != nil, url.user == nil, url.password == nil
        else { throw AppError("Artifact has no valid HTTPS download URL") }
        let expected = Int64(artifact.attributes["fileSize"].number)
        if let size = try? destination.resourceValues(forKeys: [.fileSizeKey]).fileSize, expected > 0,
            Int64(size) == expected
        {
            return destination
        }
        let temporary: URL
        let response: URLResponse
        // This session never receives the App Store Connect bearer token.
        do { (temporary, response) = try await downloadSession.download(from: url) } catch {
            try Task.checkCancellation()
            throw AppError("Artifact download failed")
        }
        defer { try? FileManager.default.removeItem(at: temporary) }
        guard let http = response as? HTTPURLResponse else { throw AppError("Invalid artifact download response") }
        if [400, 403, 410].contains(http.statusCode) { throw ExpiredArtifactURL() }
        guard (200..<300).contains(http.statusCode) else {
            throw AppError("Artifact download returned HTTP \(http.statusCode)")
        }
        let size = try temporary.resourceValues(forKeys: [.fileSizeKey]).fileSize ?? 0
        guard expected <= 0 || Int64(size) == expected else {
            throw AppError("Artifact size does not match Apple's metadata")
        }
        try Task.checkCancellation()
        try FileManager.default.createDirectory(
            at: destination.deletingLastPathComponent(), withIntermediateDirectories: true)
        let staging = destination.deletingLastPathComponent().appendingPathComponent(".download-\(UUID().uuidString)")
        defer { try? FileManager.default.removeItem(at: staging) }
        try FileManager.default.copyItem(at: temporary, to: staging)
        try FileManager.default.setAttributes([.posixPermissions: 0o600], ofItemAtPath: staging.path)
        if FileManager.default.fileExists(atPath: destination.path) {
            _ = try FileManager.default.replaceItemAt(destination, withItemAt: staging)
        } else {
            try FileManager.default.moveItem(at: staging, to: destination)
        }
        return destination
    }
}
