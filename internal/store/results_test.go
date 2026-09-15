package store

import (
	"archive/zip"
	"context"
	"github.com/rigbyworks/lazyxcode/internal/model"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func resultArchive(t *testing.T, entries map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Results.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	w := zip.NewWriter(f)
	for name, body := range entries {
		entry, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestCloudResultExpansionAndReuse(t *testing.T) {
	path := resultArchive(t, map[string]string{"Tests/Results.xcresult/Info.plist": "fixture", "Tests/Results.xcresult/Data/blob": "data"})
	bundle, err := ExpandResultBundle(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(bundle, "Data/blob"))
	if err != nil || string(data) != "data" {
		t.Fatalf("extraction: %s %v", data, err)
	}
	second, err := ExpandResultBundle(context.Background(), path)
	if err != nil || second != bundle {
		t.Fatalf("reuse: %s %v", second, err)
	}
}
func TestCloudResultExpansionRejectsUnsafeOrAmbiguousArchives(t *testing.T) {
	for _, entries := range []map[string]string{
		{"../escape": "bad", "A.xcresult/Info.plist": "info"},
		{"/absolute": "bad", "A.xcresult/Info.plist": "info"},
		{"A.xcresult/Info.plist": "one", "B.xcresult/Info.plist": "two"},
		{"logs.txt": "no result"},
	} {
		if _, err := ExpandResultBundle(context.Background(), resultArchive(t, entries)); err == nil {
			t.Fatal("accepted", entries)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ExpandResultBundle(ctx, resultArchive(t, map[string]string{"A.xcresult/Info.plist": "info"})); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestResultBundlesSurviveCacheClearAndExpireWithHistory(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p, err := NewProject(model.Container{Path: "/app"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := p.NewResultDir("old")
	if err != nil {
		t.Fatal(err)
	}
	bundle := filepath.Join(dir, "Results.xcresult")
	if err := os.Mkdir(bundle, 0700); err != nil {
		t.Fatal(err)
	}
	if err := p.ClearCache(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(bundle); err != nil {
		t.Fatal("cache clear removed results")
	}
	old := model.BuildRecord{ID: "old", Operation: model.OperationTest, Phase: model.PhaseSucceeded, ResultBundlePath: bundle, StartedAt: time.Now().Add(-time.Hour)}
	if _, err := p.Save(old); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < historyLimit; i++ {
		if _, err := p.Save(model.BuildRecord{ID: time.Unix(int64(i), 0).Format("150405"), Phase: model.PhaseSucceeded, StartedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := os.Stat(bundle); !os.IsNotExist(err) {
		t.Fatalf("expired results retained: %v", err)
	}
}
func TestHistoryDoesNotPruneActiveResultBundles(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	p, err := NewProject(model.Container{Path: "/app"})
	if err != nil {
		t.Fatal(err)
	}
	dir, err := p.NewResultDir("active")
	if err != nil {
		t.Fatal(err)
	}
	history := make([]model.BuildRecord, 0, historyLimit+1)
	for i := 0; i < historyLimit; i++ {
		history = append(history, model.BuildRecord{ID: time.Unix(int64(i), 0).Format("150405"), Phase: model.PhaseSucceeded, StartedAt: time.Now()})
	}
	history = append(history, model.BuildRecord{ID: "active", Phase: model.PhaseTesting, StartedAt: time.Now().Add(-time.Hour)})
	if err := writeJSONAtomic(filepath.Join(p.stateDir, "builds.json"), historyFile{Builds: history}); err != nil {
		t.Fatal(err)
	}
	kept, err := p.Save(history[0])
	if err != nil {
		t.Fatal(err)
	}
	if len(kept) != historyLimit+1 {
		t.Fatalf("active run pruned: %d", len(kept))
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal(err)
	}
}
