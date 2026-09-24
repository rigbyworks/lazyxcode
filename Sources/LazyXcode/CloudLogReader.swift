import Foundation

enum CloudLogReader {
    struct Excerpt: Sendable {
        let text: String
        let bytesRead: Int
        let filesRead: Int
        let truncated: Bool
    }

    static func read(_ source: URL, maxFiles: Int, maxBytes: Int) async throws -> Excerpt {
        let worker = Task.detached(priority: .utility) {
            try readSynchronously(source, maxFiles: maxFiles, maxBytes: maxBytes)
        }
        return try await withTaskCancellationHandler {
            try await worker.value
        } onCancel: {
            worker.cancel()
        }
    }

    private static func readSynchronously(_ source: URL, maxFiles: Int, maxBytes: Int) throws -> Excerpt {
        guard maxFiles > 0, maxBytes > 0 else { return Excerpt(text: "", bytesRead: 0, filesRead: 0, truncated: true) }
        var paths: [URL] = []
        var truncated = false
        let isDirectory = (try source.resourceValues(forKeys: [.isDirectoryKey])).isDirectory == true
        if isDirectory {
            let enumerator = FileManager.default.enumerator(
                at: source, includingPropertiesForKeys: [.isRegularFileKey], options: [.skipsHiddenFiles])
            while let file = enumerator?.nextObject() as? URL {
                try Task.checkCancellation()
                guard (try? file.resourceValues(forKeys: [.isRegularFileKey]).isRegularFile) == true else { continue }
                if paths.count == maxFiles {
                    truncated = true
                    break
                }
                paths.append(file)
            }
        } else {
            paths = [source]
        }

        var text = ""
        var bytesRead = 0
        var filesRead = 0
        for file in paths.sorted(by: { $0.path < $1.path }) {
            try Task.checkCancellation()
            let remaining = maxBytes - bytesRead
            if remaining <= 0 {
                truncated = true
                break
            }
            let (data, totalSize) = try readTail(file, count: remaining)
            bytesRead += data.count
            filesRead += 1
            if totalSize > UInt64(data.count) { truncated = true }
            text += "\n\(file.lastPathComponent)\n"
            text += String(data: data, encoding: .utf8) ?? "Binary artifact retained at \(file.path)"
        }
        return Excerpt(text: text, bytesRead: bytesRead, filesRead: filesRead, truncated: truncated)
    }

    private static func readTail(_ file: URL, count: Int) throws -> (Data, UInt64) {
        let handle = try FileHandle(forReadingFrom: file)
        defer { try? handle.close() }
        let size = try handle.seekToEnd()
        try handle.seek(toOffset: size > UInt64(count) ? size - UInt64(count) : 0)
        return (try handle.read(upToCount: count) ?? Data(), size)
    }
}
