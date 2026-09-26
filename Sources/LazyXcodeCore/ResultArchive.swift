import Foundation
import ZIPFoundation

public enum ResultArchive {
    private struct Extraction: Codable {
        let sourceSize: Int
        let sourceModifiedAt: TimeInterval
        let bundle: String
    }

    public static func expandAsync(_ source: URL, requireResult: Bool = true) async throws -> URL {
        let worker = Task.detached { try expand(source, requireResult: requireResult) }
        return try await withTaskCancellationHandler {
            let result = try await worker.value
            try Task.checkCancellation()
            return result
        } onCancel: {
            worker.cancel()
        }
    }
    /// Validates the entire archive before extracting into a private sibling directory.
    public static func expand(_ source: URL, requireResult: Bool = true) throws -> URL {
        let attributes = try FileManager.default.attributesOfItem(atPath: source.path)
        guard let sourceSize = (attributes[.size] as? NSNumber)?.intValue,
            let sourceModifiedAt = (attributes[.modificationDate] as? Date)?.timeIntervalSince1970
        else {
            throw AppError("Could not inspect archive")
        }
        let archive = try Archive(url: source, accessMode: .read)
        let entries = Array(archive)
        guard entries.count <= 100_000 else { throw AppError("Archive has too many entries") }
        var total: UInt64 = 0
        var bundles = Set<String>()
        for entry in entries {
            let parts = entry.path.split(separator: "/", omittingEmptySubsequences: false)
            guard !entry.path.hasPrefix("/"), !entry.path.contains("\\"), !parts.contains(".."),
                entry.type == .file || entry.type == .directory
            else { throw AppError("Archive contains an unsafe entry") }
            let size = UInt64(entry.uncompressedSize)
            guard size <= (4 << 30) - total else { throw AppError("Archive exceeds 4 GiB expanded size") }
            total += size
            if let index = parts.firstIndex(where: { $0.hasSuffix(".xcresult") && !$0.hasPrefix(".") }) {
                bundles.insert(parts[...index].joined(separator: "/"))
            }
        }
        if requireResult && bundles.count != 1 { throw AppError("Archive must contain exactly one .xcresult bundle") }
        let bundle = requireResult ? bundles.first! : ""
        let destination = source.deletingLastPathComponent()
            .appendingPathComponent(source.lastPathComponent + ".expanded")
        let marker = destination.appendingPathComponent(".lazyxcode-extraction.json")
        if let cached = try? Store.read(Extraction.self, from: marker),
            cached.sourceSize == sourceSize, cached.sourceModifiedAt == sourceModifiedAt,
            cached.bundle == bundle
        {
            let result = bundle.isEmpty ? destination : destination.appendingPathComponent(bundle)
            var isDirectory: ObjCBool = false
            if bundle.isEmpty || Store.contains(result, in: destination),
                FileManager.default.fileExists(atPath: result.path, isDirectory: &isDirectory), isDirectory.boolValue
            {
                return result
            }
        }
        let temporary = source.deletingLastPathComponent().appendingPathComponent(".extract-\(UUID().uuidString)")
        try FileManager.default.createDirectory(
            at: temporary, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
        defer { try? FileManager.default.removeItem(at: temporary) }
        for entry in entries {
            try Task.checkCancellation()
            let target = temporary.appendingPathComponent(entry.path)
            guard Store.contains(target, in: temporary) else { throw AppError("Archive path escapes destination") }
            if entry.type == .directory {
                try FileManager.default.createDirectory(
                    at: target, withIntermediateDirectories: true, attributes: [.posixPermissions: 0o700])
            } else {
                try FileManager.default.createDirectory(
                    at: target.deletingLastPathComponent(), withIntermediateDirectories: true)
                guard
                    FileManager.default.createFile(
                        atPath: target.path, contents: nil, attributes: [.posixPermissions: 0o600])
                else { throw AppError("Cannot extract archive entry") }
                let file = try FileHandle(forWritingTo: target)
                defer { try? file.close() }
                var written: UInt64 = 0
                let checksum = try archive.extract(entry) { data in
                    try Task.checkCancellation()
                    written += UInt64(data.count)
                    guard written <= UInt64(entry.uncompressedSize) else {
                        throw AppError("Archive entry exceeds reported size")
                    }
                    try file.write(contentsOf: data)
                }
                guard checksum == entry.checksum, written == UInt64(entry.uncompressedSize) else {
                    throw AppError("Archive checksum or size mismatch")
                }
            }
        }
        let result = temporary.appendingPathComponent(bundle)
        var isDirectory: ObjCBool = false
        guard FileManager.default.fileExists(atPath: result.path, isDirectory: &isDirectory), isDirectory.boolValue
        else { throw AppError("Result bundle is not a directory") }
        try Store.write(
            Extraction(sourceSize: sourceSize, sourceModifiedAt: sourceModifiedAt, bundle: bundle),
            to: temporary.appendingPathComponent(".lazyxcode-extraction.json"))
        if FileManager.default.fileExists(atPath: destination.path) {
            try FileManager.default.removeItem(at: destination)
        }
        try FileManager.default.moveItem(at: temporary, to: destination)
        return bundle.isEmpty ? destination : destination.appendingPathComponent(bundle)
    }
}
