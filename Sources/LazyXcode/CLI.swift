import Foundation
import LazyXcodeCore
import SwiftTUI
import SwiftTUICLI

enum CLI {
    static var version: String { BuildVersion.value }
    static let help = """
        Usage: lazyxcode [--help | --version | --snapshot]

        Run from a directory containing an Xcode project or workspace.
        Requires macOS 15+ and full Xcode 16.3+.

          --help, -h  Show this help
          --version   Show the installed version
          --snapshot  Render a sample workspace without Xcode or a terminal

        Keys: b build, r run, t tests, m cloud, : actions, ? help, q quit.
        """
}

@main
enum EntryPoint {
    @MainActor static func main() async {
        let arguments = Array(CommandLine.arguments.dropFirst())
        if arguments == ["--help"] || arguments == ["-h"] {
            print(CLI.help)
            return
        }
        if arguments == ["--version"] {
            print("lazyxcode \(CLI.version)")
            return
        }
        if !arguments.isEmpty && arguments != ["--snapshot"] {
            FileHandle.standardError.write(Data("lazyxcode: unknown argument \(arguments[0]); use --help\n".utf8))
            exit(2)
        }
        if arguments == ["--snapshot"] {
            let model = WorkspaceModel(containers: [
                Container(kind: .workspace, name: "Example.xcworkspace", path: "/Example.xcworkspace")
            ])
            model.scheme = "Example"
            model.schemes = ["Example"]
            model.destinations = [
                Destination(id: "example", name: "iPhone Simulator", os: "18.4", platform: "iOS Simulator")
            ]
            model.destinationID = "example"
            model.status = "Ready"
            RenderOnce.print(WorkspaceView(model: model, live: false).frame(width: 110, height: 28), width: 110)
            return
        }
        do {
            let containers = try Container.discover(in: URL(fileURLWithPath: FileManager.default.currentDirectoryPath))
            guard !containers.isEmpty else {
                throw AppError("no .xcworkspace or .xcodeproj found in the current directory")
            }
            try await XcodeClient().checkEnvironment()
            let model = WorkspaceModel(containers: containers, preferences: try Preferences.load())
            do { try await SwiftTUILauncher.run(TerminalApplication(model: model)) } catch {
                await model.shutdown()
                throw error
            }
            await model.shutdown()
        } catch {
            FileHandle.standardError.write(Data("lazyxcode: \(error.localizedDescription)\n".utf8))
            exit(1)
        }
    }
}
