package xcode

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// CheckEnvironment checks runtime prerequisites without changing Xcode selection.
func (c *Client) CheckEnvironment(ctx context.Context) error {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return fmt.Errorf("lazyxcode requires an Apple Silicon Mac")
	}
	out, err := c.runner.Output(ctx, "sw_vers", "-productVersion")
	if err != nil {
		return commandError("check macOS version", out, err)
	}
	if !versionAtLeast(string(out), 15, 0) {
		return fmt.Errorf("macOS 15 or newer is required; found %s", strings.TrimSpace(string(out)))
	}
	out, err = c.runner.Output(ctx, "xcode-select", "-p")
	if err != nil {
		return commandError("locate Xcode; install full Xcode and select it with xcode-select or DEVELOPER_DIR", out, err)
	}
	developer := strings.TrimSpace(string(out))
	if !filepath.IsAbs(developer) {
		return fmt.Errorf("invalid Xcode developer directory %q; check DEVELOPER_DIR and xcode-select", developer)
	}
	if _, err := os.Stat(filepath.Join(developer, "usr", "bin", "xcodebuild")); err != nil {
		return fmt.Errorf("full Xcode is required at %s; select an Xcode.app with xcode-select or DEVELOPER_DIR", developer)
	}
	out, err = c.runner.Output(ctx, "xcodebuild", "-version")
	if err != nil {
		return commandError("check Xcode; complete its first-launch setup", out, err)
	}
	line, _, _ := strings.Cut(string(out), "\n")
	if !strings.HasPrefix(line, "Xcode ") || !versionAtLeast(strings.TrimPrefix(line, "Xcode "), 16, 3) {
		return fmt.Errorf("Xcode 16.3 or newer is required; found %s", line)
	}
	return nil
}

func versionAtLeast(value string, major, minor int) bool {
	var gotMajor, gotMinor int
	if _, err := fmt.Sscanf(strings.TrimSpace(value), "%d.%d", &gotMajor, &gotMinor); err != nil {
		return false
	}
	return gotMajor > major || gotMajor == major && gotMinor >= minor
}
