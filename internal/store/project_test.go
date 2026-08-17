package store

import (
	"os"
	"path/filepath"
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
