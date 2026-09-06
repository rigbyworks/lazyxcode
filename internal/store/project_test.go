package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mwahlig/lazy-xcode/internal/model"
)

func testProject(t *testing.T) *Project {
	t.Helper()
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(t.TempDir(), "cache"))
	project, err := NewProject(model.Container{Kind: model.Project, Path: "/tmp/App.xcodeproj"})
	if err != nil {
		t.Fatal(err)
	}
	return project
}

func TestSaveAndLoadBuildHistory(t *testing.T) {
	project := testProject(t)
	record := model.BuildRecord{ID: "one", Phase: model.PhaseSucceeded, StartedAt: time.Now()}
	if _, err := project.Save(record); err != nil {
		t.Fatal(err)
	}
	got, err := project.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "one" {
		t.Fatalf("history = %#v", got)
	}
}

func TestLoadMarksInterruptedBuildCancelled(t *testing.T) {
	project := testProject(t)
	record := model.BuildRecord{ID: "active", Phase: model.PhaseBuilding, StartedAt: time.Now()}
	if _, err := project.Save(record); err != nil {
		t.Fatal(err)
	}
	got, err := project.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Phase != model.PhaseCancelled || got[0].FinishedAt == nil {
		t.Fatalf("interrupted record = %#v", got[0])
	}
}

func TestHistoryPrunesOldestBuildsAndLogs(t *testing.T) {
	project := testProject(t)
	var oldestLog string
	for i := 0; i <= historyLimit; i++ {
		id := time.Unix(int64(i), 0).Format("150405")
		file, path, err := project.NewLog(id)
		if err != nil {
			t.Fatal(err)
		}
		file.Close()
		if i == 0 {
			oldestLog = path
		}
		if _, err := project.Save(model.BuildRecord{ID: id, Phase: model.PhaseSucceeded, StartedAt: time.Unix(int64(i), 0), LogPath: path}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := project.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != historyLimit {
		t.Fatalf("history length = %d", len(history))
	}
	if _, err := os.Stat(oldestLog); !os.IsNotExist(err) {
		t.Fatalf("oldest log still exists: %v", err)
	}
}

func TestClearCacheOnlyRemovesManagedDerivedData(t *testing.T) {
	project := testProject(t)
	derived := project.DerivedData("App", "AAAA")
	if err := os.MkdirAll(derived, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(derived, "artifact"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := project.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(derived); !os.IsNotExist(err) {
		t.Fatalf("derived data still exists: %v", err)
	}
}

func TestCloudArtifactPathsStayInsideTheCloudCache(t *testing.T) {
	project := testProject(t)
	cases := map[string][3]string{
		"normal":    {"run-245", "artifact-1", "Logs.zip"},
		"traversal": {"../../etc", "artifact-2", "../../../passwd"},
		"absolute":  {"/tmp/run", "artifact-3", "/etc/hosts"},
		"dotfiles":  {"..", "artifact-4", "..."},
		"spaces":    {"run 1", "artifact-5", "Test Results.xcresult.zip"},
	}
	root := filepath.Join(project.cacheDir, "cloud") + string(os.PathSeparator)
	for name, input := range cases {
		path, err := project.CloudArtifactPath(input[0], input[1], input[2])
		if name == "dotfiles" {
			if err == nil {
				t.Fatalf("%s: expected an error, got %q", name, path)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !strings.HasPrefix(path, root) || strings.Contains(path, "..") {
			t.Fatalf("%s: unsafe path %q", name, path)
		}
		if filepath.Base(filepath.Dir(filepath.Dir(path))) != "artifacts" {
			t.Fatalf("%s: unexpected layout %q", name, path)
		}
	}
	path, _ := project.CloudArtifactPath("run-245", "artifact-1", "Logs.zip")
	if filepath.Base(path) != "Logs.zip" {
		t.Fatalf("plain filename was altered: %q", path)
	}
	other, _ := project.CloudArtifactPath("run-245", "artifact-2", "Logs.zip")
	if path == other {
		t.Fatalf("artifacts with the same filename share a path: %q", path)
	}
	if err := project.ClearCache(); err != nil {
		t.Fatal(err)
	}
}
