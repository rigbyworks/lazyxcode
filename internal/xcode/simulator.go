package xcode

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

func (c *Client) openSimulator(ctx context.Context, id string) error {
	// xcode-select also resolves DEVELOPER_DIR overrides, including .app paths.
	out, err := c.runner.Output(ctx, "xcode-select", "-p")
	if err != nil {
		return commandError("locate selected Xcode", out, err)
	}
	developer := strings.TrimSpace(string(out))
	if !filepath.IsAbs(developer) {
		return fmt.Errorf("locate selected Xcode: invalid developer directory %q", developer)
	}
	app := filepath.Join(filepath.Dir(developer), "Applications", "DeviceHub.app")
	info, err := os.Stat(app)
	var args []string
	switch {
	case err == nil && info.IsDir():
		// Send a device URL even when the hub is already running. A plain app
		// activation can restore a different device or display window.
		args = []string{"-a", app, "devices://device/open?id=" + url.QueryEscape(id)}
	case os.IsNotExist(err):
		app = filepath.Join(developer, "Applications", "Simulator.app")
		info, err = os.Stat(app)
		if err != nil {
			return fmt.Errorf("locate Simulator in selected Xcode: %w", err)
		}
		if !info.IsDir() {
			return fmt.Errorf("Simulator is not an application directory: %s", app)
		}
		args = []string{"-a", app, "--args", "-CurrentDeviceUDID", id, "-AttachBootedOnStart", "NO"}
	case err != nil:
		return fmt.Errorf("locate Device Hub in selected Xcode: %w", err)
	default:
		return fmt.Errorf("Device Hub is not an application directory: %s", app)
	}
	if out, err := c.runner.Output(ctx, "open", args...); err != nil {
		return commandError("open "+filepath.Base(app), out, err)
	}
	return nil
}
