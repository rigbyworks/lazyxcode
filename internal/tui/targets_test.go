package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/mwahlig/lazy-xcode/internal/xcode"
)

type targetRunner struct {
	output string
	err    error
}

func (r *targetRunner) Output(_ context.Context, name string, _ ...string) ([]byte, error) {
	if name == "xcodebuild" {
		return []byte(r.output), r.err
	}
	return []byte(`{"devices":{}}`), nil
}

func (*targetRunner) Stream(context.Context, io.Writer, string, ...string) error { return nil }

func TestTargetPickerDiscoversNewDeviceWithoutReload(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.ctx = t.Context()
	a.xcode = xcode.New(&targetRunner{output: `Available destinations for the "App" scheme:
{ platform:iOS, id:IPAD, name:iPad Pro, OS:27.0 }
{ platform:iOS, id:PHONE, name:iPhone 17 Pro, OS:27.0 }`})
	a.configRow = 1
	a.status = "Building App"
	h.press(a.openConfigPicker)
	a.overlay.filter = "iP"
	select {
	case update := <-h.updates:
		update()
	case <-time.After(time.Second):
		t.Fatal("opening the target picker did not refresh available devices")
	}
	if len(a.sims) != 2 || a.sims[a.simulator].ID != "PHONE" {
		t.Fatalf("targets or selection = %+v, index %d", a.sims, a.simulator)
	}
	items := a.filteredOverlayItems()
	if len(items) != 2 || items[a.overlay.selected].ID != "PHONE" || a.overlay.filter != "iP" {
		t.Fatalf("picker lost selection or filter: %+v", a.overlay)
	}
	if a.loading || a.status != "Building App" {
		t.Fatalf("refresh disrupted activity: loading=%v status=%q", a.loading, a.status)
	}
	h.layout()
	if err := a.chooseOverlay(h.gui, nil); err != nil {
		t.Fatal(err)
	}
	if a.sims[a.simulator].ID != "PHONE" {
		t.Fatal("picker selected the wrong device after refresh")
	}
}

func awaitTargetUpdate(t *testing.T, h *cloudHarness) func() {
	t.Helper()
	select {
	case update := <-h.updates:
		return update
	case <-time.After(time.Second):
		t.Fatal("target discovery did not finish")
		return nil
	}
}

func TestTargetsPollWhilePickerStaysOpen(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.ctx = t.Context()
	runner := &targetRunner{output: `Available destinations for the "App" scheme:
{ platform:iOS, id:PHONE, name:iPhone 17 Pro, OS:27.0 }`}
	a.xcode = xcode.New(runner)
	a.configRow = 1
	h.press(a.openConfigPicker)
	awaitTargetUpdate(t, h)()
	a.overlay.filter = "iPad"
	runner.output += "\n{ platform:iOS, id:IPAD, name:iPad Pro, OS:27.0 }"
	now := a.clock()
	a.targetTick(now.Add(14 * time.Second))
	if a.targetRefreshCancel != nil {
		t.Fatal("polled before the refresh interval elapsed")
	}
	a.targetTick(now.Add(15 * time.Second))
	if a.loading {
		t.Fatal("background discovery blocked local actions")
	}
	awaitTargetUpdate(t, h)()
	if items := a.filteredOverlayItems(); len(items) != 1 || items[0].ID != "IPAD" {
		t.Fatalf("newly available iPad missing from open picker: %+v", items)
	}
	h.press(a.chooseOverlay)
	if a.sims[a.simulator].ID != "IPAD" {
		t.Fatal("newly discovered iPad cannot be selected")
	}
}

func TestTargetRefreshRetainsListOnFailureAndRetries(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.ctx = t.Context()
	runner := &targetRunner{err: errors.New("device service unavailable")}
	a.xcode = xcode.New(runner)
	a.refreshTargets()
	awaitTargetUpdate(t, h)()
	if len(a.sims) != 1 || a.sims[0].ID != "PHONE" {
		t.Fatalf("failed discovery discarded targets: %+v", a.sims)
	}
	runner.err = nil
	a.targetTick(a.clock().Add(15 * time.Second))
	awaitTargetUpdate(t, h)()
	if len(a.sims) != 0 || a.simulator != 0 {
		t.Fatalf("successful empty discovery kept unavailable targets: %+v", a.sims)
	}
	a.status = "No compatible targets"
	runner.output = `Available destinations for the "App" scheme:
{ platform:iOS, id:IPAD, name:iPad Pro, OS:27.0 }`
	a.targetTick(a.clock().Add(30 * time.Second))
	awaitTargetUpdate(t, h)()
	if len(a.sims) != 1 || a.sims[a.simulator].ID != "IPAD" {
		t.Fatalf("discovery did not recover from empty list: %+v", a.sims)
	}
	if !strings.Contains(a.status, "1 devices") {
		t.Fatalf("status did not recover with target list: %q", a.status)
	}
}

func TestTargetRefreshDoesNotOverlapOrApplyOldSchemeResults(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.ctx = t.Context()
	a.project = nil
	a.xcode = xcode.New(&targetRunner{output: `Available destinations for the "App" scheme:
{ platform:iOS, id:IPAD, name:iPad Pro, OS:27.0 }`})
	a.refreshTargets()
	oldUpdate := awaitTargetUpdate(t, h)
	// A completed command remains in flight until the UI processes its result.
	a.targetTick(a.clock().Add(time.Hour))
	select {
	case <-h.updates:
		t.Fatal("started an overlapping discovery")
	case <-time.After(50 * time.Millisecond):
	}
	a.schemes = []string{"Other"}
	a.loadSimulators()
	oldUpdate()
	if len(a.sims) != 0 || !a.loading {
		t.Fatal("old scheme discovery overwrote the new scheme's loading state")
	}
	awaitTargetUpdate(t, h)()
}

func TestTargetRefreshPreservesChoiceMadeDuringDiscovery(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.ctx = t.Context()
	a.xcode = xcode.New(&targetRunner{output: `Available destinations for the "App" scheme:
{ platform:iOS, id:IPAD, name:iPad Pro, OS:27.0 }
{ platform:iOS, id:PHONE, name:iPhone 17 Pro, OS:27.0 }`})
	a.refreshTargets()
	awaitTargetUpdate(t, h)()
	a.refreshTargets()
	update := awaitTargetUpdate(t, h)
	a.simulator = 0 // Choose the iPad while discovery is running.
	update()
	if a.sims[a.simulator].ID != "IPAD" {
		t.Fatal("refresh reverted a newer target selection")
	}
}

func TestDisconnectedTargetIsRemovedFromOpenPicker(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.ctx = t.Context()
	a.xcode = xcode.New(&targetRunner{output: `Available destinations for the "App" scheme:
{ platform:iOS, id:IPAD, name:iPad Pro, OS:27.0 }`})
	a.configRow = 1
	h.press(a.openConfigPicker)
	awaitTargetUpdate(t, h)()
	if len(a.sims) != 1 || a.sims[a.simulator].ID != "IPAD" {
		t.Fatalf("disconnected target retained: %+v", a.sims)
	}
	h.press(a.chooseOverlay)
	if a.sims[a.simulator].ID != "IPAD" {
		t.Fatal("picker still selected the disconnected phone")
	}
}

func TestTargetPollingPausesDuringCloudModeAndMetadataLoading(t *testing.T) {
	h := newCloudHarness(t, nil, 120, 36)
	a := h.app
	a.ctx = t.Context()
	a.xcode = xcode.New(&targetRunner{})
	a.mode = modeCloud
	a.targetTick(a.clock())
	if a.targetRefreshCancel != nil {
		t.Fatal("started local target discovery in Cloud mode")
	}
	a.mode = modeLocal
	a.loading = true
	a.targetTick(a.clock())
	if a.targetRefreshCancel != nil {
		t.Fatal("started discovery while loading project metadata")
	}
	a.loading = false
	a.targetTick(a.clock())
	awaitTargetUpdate(t, h)()
}
