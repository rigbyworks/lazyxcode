import Darwin
import Foundation
import Synchronization

public protocol CommandRunning: Sendable {
    @discardableResult
    func run(
        _ executable: String, _ arguments: [String],
        output: (@Sendable (Data) -> Void)?
    ) async throws -> Data
}

extension CommandRunning {
    @discardableResult
    public func run(_ executable: String, _ arguments: [String]) async throws -> Data {
        try await run(executable, arguments, output: nil)
    }
}

/// Owns one process across the launch/cancellation race. All process state is locked.
private final class ProcessHandle: Sendable {
    struct State {
        var process: Process?
        var cancelled = false
    }
    let state = Mutex(State())

    func launch(_ process: Process) throws {
        try state.withLock { state in
            guard !state.cancelled else { throw CancellationError() }
            try process.run()
            state.process = process
        }
    }

    func cancel() {
        state.withLock { state in
            state.cancelled = true
            guard let process = state.process, process.isRunning else { return }
            signal(process, SIGTERM)
        }
        DispatchQueue.global().asyncAfter(deadline: .now() + 2) { [self] in
            state.withLock { state in
                if let process = state.process, process.isRunning { signal(process, SIGKILL) }
            }
        }
    }

    private func signal(_ process: Process, _ signal: Int32) {
        let pid = process.processIdentifier
        // Foundation gives subprocesses their own group. Never signal our own group.
        if getpgid(pid) == pid { _ = Darwin.kill(-pid, signal) } else { _ = Darwin.kill(pid, signal) }
    }
}

public struct CommandRunner: CommandRunning {
    public init() {}

    public func run(
        _ executable: String, _ arguments: [String],
        output: (@Sendable (Data) -> Void)?
    ) async throws -> Data {
        let handle = ProcessHandle()
        return try await withTaskCancellationHandler {
            try Task.checkCancellation()
            let data: Data = try await withCheckedThrowingContinuation { continuation in
                DispatchQueue.global(qos: .userInitiated).async {
                    let process = Process()
                    process.executableURL = URL(
                        fileURLWithPath: executable.hasPrefix("/") ? executable : "/usr/bin/\(executable)")
                    process.arguments = arguments
                    process.environment = ProcessInfo.processInfo.environment.merging([
                        "NSUnbufferedIO": "YES", "SIMCTL_CHILD_NSUnbufferedIO": "YES",
                        "DEVICECTL_CHILD_NSUnbufferedIO": "YES",
                    ]) { _, value in value }
                    process.standardInput = FileHandle.nullDevice
                    let pipe = Pipe()
                    process.standardOutput = pipe
                    let errorPipe = output == nil ? Pipe() : nil
                    process.standardError = errorPipe ?? pipe
                    let errors = Mutex((first: Data(), tail: Data()))
                    let errorDrain = DispatchGroup()
                    do {
                        try handle.launch(process)
                        if let errorPipe {
                            errorDrain.enter()
                            DispatchQueue.global().async {
                                defer {
                                    errorDrain.leave()
                                    try? errorPipe.fileHandleForReading.close()
                                }
                                while true {
                                    let chunk = errorPipe.fileHandleForReading.availableData
                                    if chunk.isEmpty { break }
                                    errors.withLock { state in
                                        if state.first.count < 4096 {
                                            state.first.append(chunk.prefix(4096 - state.first.count))
                                        }
                                        state.tail.append(chunk)
                                        if state.tail.count > 8192 { state.tail = state.tail.suffix(8192) }
                                    }
                                }
                            }
                        }
                        var captured = Data()
                        var tail = Data()
                        var overflow = false
                        while true {
                            let chunk = pipe.fileHandleForReading.availableData
                            if chunk.isEmpty { break }
                            if let output {
                                output(chunk)
                            } else if captured.count + chunk.count <= 32 * 1024 * 1024 {
                                captured.append(chunk)
                            } else {
                                overflow = true
                                handle.cancel()
                            }
                            tail.append(chunk)
                            if tail.count > 8192 { tail = tail.suffix(8192) }
                        }
                        process.waitUntilExit()
                        errorDrain.wait()
                        try pipe.fileHandleForReading.close()
                        if overflow { throw AppError("\(executable) output exceeded 32 MiB") }
                        if handle.state.withLock({ $0.cancelled }) { throw CancellationError() }
                        guard process.terminationStatus == 0 else {
                            let stderr = errors.withLock { state in
                                state.tail.starts(with: state.first)
                                    ? state.tail : state.first + Data("\n…\n".utf8) + state.tail
                            }
                            throw AppError(
                                "\(executable) exited \(process.terminationStatus):\n\(String(decoding: tail + stderr, as: UTF8.self))"
                            )
                        }
                        continuation.resume(returning: captured)
                    } catch { continuation.resume(throwing: error) }
                }
            }
            try Task.checkCancellation()
            return data
        } onCancel: {
            handle.cancel()
        }
    }
}

/// Complete transcript on disk, bounded tail in memory. Producers never queue UI updates.
public final class ActivityLog: Sendable {
    struct State {
        var data = Data()
        var version = 0
        var failure: String?
        var handle: FileHandle
        var summary = BuildOutput()
    }
    private let state: Mutex<State>
    public init(url: URL) throws {
        try FileManager.default.createDirectory(at: url.deletingLastPathComponent(), withIntermediateDirectories: true)
        if !FileManager.default.fileExists(atPath: url.path) {
            guard
                FileManager.default.createFile(atPath: url.path, contents: nil, attributes: [.posixPermissions: 0o600])
            else {
                throw AppError("Could not create activity log")
            }
        }
        let handle = try FileHandle(forWritingTo: url)
        try handle.seekToEnd()
        state = Mutex(State(handle: handle))
    }
    deinit { state.withLock { try? $0.handle.close() } }
    public func append(_ data: Data) {
        state.withLock { state in
            var recorded = Data()
            var start = data.startIndex
            let now = Date()
            for index in data.indices where data[index] == 10 {
                let end = data.index(after: index)
                let line = Data(data[start..<end])
                recorded.append(line)
                recorded.append(state.summary.append(line, at: now))
                start = end
            }
            if start < data.endIndex {
                let partial = Data(data[start...])
                recorded.append(partial)
                state.summary.append(partial, at: now)
            }
            do { try state.handle.write(contentsOf: recorded) } catch { state.failure = error.localizedDescription }
            state.data.append(recorded)
            if state.data.count > 256 * 1024 { state.data = state.data.suffix(256 * 1024) }
            state.version += 1
        }
    }
    public var version: Int { state.withLock { $0.version } }
    public func finish() {
        state.withLock { state in
            var markers = state.summary.finish()
            if !markers.isEmpty && state.data.last != 10 { markers.insert(10, at: 0) }
            do { try state.handle.write(contentsOf: markers) } catch { state.failure = error.localizedDescription }
            state.data.append(markers)
            state.version += 1
        }
    }
    public func snapshot() -> (text: String, version: Int, error: String?) {
        state.withLock { (String(decoding: $0.data, as: UTF8.self), $0.version, $0.failure) }
    }
    public func summary() -> BuildOutput { state.withLock { $0.summary } }
}
