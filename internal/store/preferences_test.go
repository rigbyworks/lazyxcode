package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func testPreferences(t *testing.T, existing string) *Preferences {
	t.Helper()
	root := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", root)
	if existing != "" {
		if err := os.MkdirAll(filepath.Join(root, "lazy-xcode"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "lazy-xcode", "preferences.json"), []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	preferences, err := NewPreferences()
	if err != nil {
		t.Fatal(err)
	}
	return preferences
}

func TestPreferencesLoadVersionOneFilesWithoutCloudMaps(t *testing.T) {
	preferences := testPreferences(t, `{"version":1,"containers":{"/dir":"/dir/App.xcodeproj"},"schemes":{"/dir/App.xcodeproj":"App"},"simulators":{}}`)
	if preferences.Container("/dir") != "/dir/App.xcodeproj" || preferences.Scheme("/dir/App.xcodeproj") != "App" {
		t.Fatalf("legacy preferences were not preserved: %#v", preferences.data)
	}
	if preferences.CloudProduct("/dir/App.xcodeproj") != "" || preferences.CloudWorkflow("/dir/App.xcodeproj") != "" {
		t.Fatal("cloud selections should be empty for version-1 files")
	}
	if err := preferences.SetCloudProduct("/dir/App.xcodeproj", "prod-1"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(preferences.path)
	if err != nil {
		t.Fatal(err)
	}
	var stored map[string]any
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["version"] != float64(preferencesVersion) || stored["containers"].(map[string]any)["/dir"] != "/dir/App.xcodeproj" {
		t.Fatalf("stored preferences = %v", stored)
	}
}

func TestPreferencesRoundTripCloudSelectionsPerContainer(t *testing.T) {
	preferences := testPreferences(t, "")
	if err := preferences.SetCloudProduct("/a/App.xcodeproj", "prod-a"); err != nil {
		t.Fatal(err)
	}
	if err := preferences.SetCloudWorkflow("/a/App.xcodeproj", "wf-a"); err != nil {
		t.Fatal(err)
	}
	if err := preferences.SetCloudProduct("/b/App.xcworkspace", "prod-b"); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewPreferences()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.CloudProduct("/a/App.xcodeproj") != "prod-a" || reloaded.CloudWorkflow("/a/App.xcodeproj") != "wf-a" {
		t.Fatalf("container a selections = %q/%q", reloaded.CloudProduct("/a/App.xcodeproj"), reloaded.CloudWorkflow("/a/App.xcodeproj"))
	}
	if reloaded.CloudProduct("/b/App.xcworkspace") != "prod-b" || reloaded.CloudWorkflow("/b/App.xcworkspace") != "" {
		t.Fatalf("container b selections = %q/%q", reloaded.CloudProduct("/b/App.xcworkspace"), reloaded.CloudWorkflow("/b/App.xcworkspace"))
	}
	if err := reloaded.SetCloudWorkflow("/a/App.xcodeproj", ""); err != nil {
		t.Fatal(err)
	}
	if reloaded.CloudWorkflow("/a/App.xcodeproj") != "" {
		t.Fatal("clearing the workflow filter did not remove it")
	}
}
