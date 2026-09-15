package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/rigbyworks/lazyxcode/internal/app"
)

var version = "dev"

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr, app.Run))
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer, start func(context.Context) error) int {
	if len(args) == 1 {
		switch args[0] {
		case "--help", "-h":
			fmt.Fprintln(stdout, "Usage: lazyxcode [--help | --version]\n\nRun from a directory containing an Xcode project or workspace.\nRequires Apple Silicon, macOS 15+, and full Xcode 16.3+.\n\n  --help, -h  Show this help\n  --version   Show the installed version")
			return 0
		case "--version":
			fmt.Fprintln(stdout, "lazyxcode", buildVersion())
			return 0
		}
	}
	if len(args) != 0 {
		fmt.Fprintf(stderr, "lazyxcode: unknown argument %q; use --help\n", args[0])
		return 2
	}
	if err := start(ctx); err != nil {
		fmt.Fprintln(stderr, "lazyxcode:", err)
		return 1
	}
	return 0
}

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
