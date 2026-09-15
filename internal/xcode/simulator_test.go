package xcode

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mwahlig/lazy-xcode/internal/model"
)

func simulatorFrontendFixture(t *testing.T, hub bool) (string, string) {
	t.Helper()
	contents := filepath.Join(t.TempDir(), "Selected Xcode.app", "Contents")
	developer := filepath.Join(contents, "Developer")
	app := filepath.Join(developer, "Applications", "Simulator.app")
	if hub {
		app = filepath.Join(contents, "Applications", "DeviceHub.app")
	}
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	return developer, app
}

func TestBootUsesSelectedXcodeDeviceHub(t *testing.T) {
	developer, app := simulatorFrontendFixture(t, true)
	// Even when both apps exist, Device Hub must win.
	if err := os.MkdirAll(filepath.Join(developer, "Applications", "Simulator.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: map[string][]byte{"xcode-select -p": []byte(developer + "\n")}}
	if err := New(runner).Boot(context.Background(), model.Simulator{ID: "PHONE"}); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"xcrun simctl boot PHONE",
		"xcode-select -p",
		"open -a " + app + " devices://device/open?id=PHONE",
		"xcrun simctl bootstatus PHONE -b",
	}
	if !reflect.DeepEqual(runner.calls, want) {
		t.Fatalf("calls = %#v, want %#v", runner.calls, want)
	}
}

func TestBootDoesNotFallbackWhenDeviceHubFailsToOpen(t *testing.T) {
	developer, _ := simulatorFrontendFixture(t, true)
	if err := os.MkdirAll(filepath.Join(developer, "Applications", "Simulator.app"), 0o755); err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{
		outputs: map[string][]byte{"xcode-select -p": []byte(developer)},
		errors:  map[string]error{"open -a": errors.New("launch failed")},
	}
	err := New(runner).Boot(context.Background(), model.Simulator{ID: "PHONE"})
	if err == nil || !strings.Contains(err.Error(), "DeviceHub.app") {
		t.Fatalf("error = %v", err)
	}
	if len(runner.calls) != 3 || strings.Contains(strings.Join(runner.calls, "\n"), "Simulator.app") {
		t.Fatalf("unexpected fallback or boot wait: %#v", runner.calls)
	}
}

func TestBootRejectsUnavailableSelectedXcodeFrontend(t *testing.T) {
	for _, selection := range []string{"", "relative/path", filepath.Join(t.TempDir(), "Developer")} {
		t.Run(selection, func(t *testing.T) {
			runner := &fakeRunner{outputs: map[string][]byte{"xcode-select -p": []byte(selection)}}
			if err := New(runner).Boot(context.Background(), model.Simulator{ID: "PHONE"}); err == nil {
				t.Fatal("expected missing frontend error")
			}
			if len(runner.calls) != 2 {
				t.Fatalf("opened an unrelated Xcode application: %#v", runner.calls)
			}
		})
	}
}

func TestBootPropagatesXcodeSelectionFailure(t *testing.T) {
	runner := &fakeRunner{errors: map[string]error{"xcode-select -p": errors.New("Xcode not selected")}}
	err := New(runner).Boot(context.Background(), model.Simulator{ID: "PHONE"})
	if err == nil || !strings.Contains(err.Error(), "locate selected Xcode") || len(runner.calls) != 2 {
		t.Fatalf("error = %v, calls = %#v", err, runner.calls)
	}
}
