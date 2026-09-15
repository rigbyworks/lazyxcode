package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rigbyworks/lazyxcode/internal/model"
)

const historyLimit = 100

type Project struct {
	mu        sync.Mutex
	container model.Container
	stateDir  string
	cacheDir  string
}

type historyFile struct {
	Version int                 `json:"version"`
	Builds  []model.BuildRecord `json:"builds"`
}

func NewProject(container model.Container) (*Project, error) {
	state, err := stateRoot()
	if err != nil {
		return nil, err
	}
	cache, err := cacheRoot()
	if err != nil {
		return nil, err
	}
	key := shortHash(string(container.Kind) + "\x00" + container.Path)
	return &Project{
		container: container,
		stateDir:  filepath.Join(state, "projects", key),
		cacheDir:  filepath.Join(cache, "projects", key),
	}, nil
}

func stateRoot() (string, error) {
	if root := os.Getenv("XDG_STATE_HOME"); root != "" {
		return filepath.Join(root, "lazyxcode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "lazyxcode"), nil
}

func cacheRoot() (string, error) {
	if root := os.Getenv("XDG_CACHE_HOME"); root != "" {
		return filepath.Join(root, "lazyxcode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "lazyxcode"), nil
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
}

// CloudRunDir returns the managed cache directory for one Xcode Cloud run.
func (p *Project) CloudRunDir(runID string) (string, error) {
	safeRun, err := safePathComponent(runID)
	if err != nil {
		return "", fmt.Errorf("invalid run ID: %w", err)
	}
	return filepath.Join(p.cacheDir, "cloud", safeRun), nil
}

// CloudArtifactPath returns a safe, artifact-specific download destination.
// Hostile run IDs and filenames are sanitized so that the result always stays
// inside the project's cloud cache directory.
func (p *Project) CloudArtifactPath(runID, artifactID, filename string) (string, error) {
	dir, err := p.CloudRunDir(runID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(artifactID) == "" {
		return "", errors.New("empty artifact ID")
	}
	safeName, err := safePathComponent(filename)
	if err != nil {
		return "", fmt.Errorf("invalid artifact name: %w", err)
	}
	path := filepath.Join(dir, "artifacts", shortHash(artifactID), safeName)
	if !strings.HasPrefix(path, filepath.Clean(dir)+string(os.PathSeparator)) {
		return "", errors.New("artifact path escapes the cloud cache")
	}
	return path, nil
}

const maxPathComponent = 128

func safePathComponent(value string) (string, error) {
	base := filepath.Base(strings.ReplaceAll(value, "\\", "/"))
	var builder strings.Builder
	for _, r := range base {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteRune('_')
		}
	}
	result := strings.TrimLeft(builder.String(), ".")
	if len(result) > maxPathComponent {
		result = result[:maxPathComponent]
	}
	if result == "" || result == "." || result == ".." {
		return "", errors.New("empty after sanitizing")
	}
	return result, nil
}

func (p *Project) DerivedData(scheme, simulator string) string {
	return filepath.Join(p.cacheDir, "derived-data", shortHash(scheme+"\x00"+simulator))
}

func (p *Project) NewLog(id string) (*os.File, string, error) {
	path := filepath.Join(p.stateDir, "logs", id+".log")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	return file, path, err
}

func (p *Project) openLog(record model.BuildRecord) (*os.File, error) {
	clean := filepath.Clean(record.LogPath)
	logs := filepath.Join(p.stateDir, "logs") + string(os.PathSeparator)
	if !strings.HasPrefix(clean, logs) {
		return nil, errors.New("build log is outside the project state directory")
	}
	return os.Open(clean)
}

// ReadLogPage reads at most maxBytes before end. A negative end selects the
// current end of the file. Returned offsets allow paging without retaining pages.
func (p *Project) ReadLogPage(record model.BuildRecord, end int64, maxBytes int) ([]byte, int64, int64, error) {
	file, err := p.openLog(record)
	if err != nil {
		return nil, 0, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, 0, err
	}
	if end < 0 || end > info.Size() {
		end = info.Size()
	}
	start := max(int64(0), end-int64(max(0, maxBytes)))
	data := make([]byte, int(end-start))
	n, err := file.ReadAt(data, start)
	if err == io.EOF {
		err = nil
	}
	return data[:n], start, start + int64(n), err
}

// CopyLog reads a persisted transcript with bounded memory. Cancellation lets
// the UI stop a historical load immediately when selection changes.
func (p *Project) CopyLog(ctx context.Context, record model.BuildRecord, destination io.Writer) error {
	if record.LogPath == "" {
		return nil
	}
	file, err := p.openLog(record)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer file.Close()
	buffer := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := file.Read(buffer)
		if n > 0 {
			written, writeErr := destination.Write(buffer[:n])
			if writeErr != nil {
				return writeErr
			}
			if written != n {
				return io.ErrShortWrite
			}
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (p *Project) Load() ([]model.BuildRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	data, err := os.ReadFile(filepath.Join(p.stateDir, "builds.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var history historyFile
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, err
	}
	changed := false
	for i := range history.Builds {
		if history.Builds[i].Phase.Active() {
			now := time.Now()
			history.Builds[i].Phase = model.PhaseCancelled
			history.Builds[i].Error = "lazyxcode exited before the activity completed"
			history.Builds[i].FinishedAt = &now
			changed = true
		}
	}
	if changed {
		if err := writeJSONAtomic(filepath.Join(p.stateDir, "builds.json"), history); err != nil {
			return nil, err
		}
	}
	return history.Builds, nil
}

func (p *Project) Save(record model.BuildRecord) ([]model.BuildRecord, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	history, err := p.loadUnlocked()
	if err != nil {
		return nil, err
	}
	found := false
	for i := range history {
		if history[i].ID == record.ID {
			history[i] = record
			found = true
			break
		}
	}
	if !found {
		history = append(history, record)
	}
	sort.SliceStable(history, func(i, j int) bool { return history[i].StartedAt.After(history[j].StartedAt) })
	var expired []model.BuildRecord
	if len(history) > historyLimit {
		kept := make([]model.BuildRecord, 0, historyLimit)
		for _, old := range history {
			if len(kept) < historyLimit || old.Phase.Active() {
				kept = append(kept, old)
			} else {
				expired = append(expired, old)
			}
		}
		history = kept
	}
	if err := writeJSONAtomic(filepath.Join(p.stateDir, "builds.json"), historyFile{Version: 1, Builds: history}); err != nil {
		return nil, err
	}
	for _, old := range expired {
		if old.ID != "" && filepath.Base(old.ID) == old.ID && old.ID != "." && old.ID != ".." {
			_ = os.RemoveAll(filepath.Join(p.stateDir, "results", old.ID))
		}
		logs := filepath.Join(p.stateDir, "logs") + string(os.PathSeparator)
		if old.LogPath != "" && strings.HasPrefix(filepath.Clean(old.LogPath), logs) {
			_ = os.Remove(old.LogPath)
		}
	}

	return history, nil
}

func (p *Project) loadUnlocked() ([]model.BuildRecord, error) {
	data, err := os.ReadFile(filepath.Join(p.stateDir, "builds.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var history historyFile
	if err := json.Unmarshal(data, &history); err != nil {
		return nil, err
	}
	return history.Builds, nil
}

func (p *Project) ClearCache() error {
	root := filepath.Clean(p.cacheDir)
	if root == "." || root == string(os.PathSeparator) {
		return fmt.Errorf("refusing unsafe cache path %q", root)
	}
	return os.RemoveAll(filepath.Join(root, "derived-data"))
}

func (p *Project) CacheSize() (int64, error) {
	var size int64
	err := filepath.WalkDir(filepath.Join(p.cacheDir, "derived-data"), func(path string, entry os.DirEntry, err error) error {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			size += info.Size()
		}
		return nil
	})
	return size, err
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer os.Remove(tempName)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(append(data, '\n')); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, path)
}

// NewResultDir allocates a directory outside DerivedData so cache clearing
// preserves test results. Only activity-owned directories are pruned.
func (p *Project) NewResultDir(id string) (string, error) {
	if id == "" || id != filepath.Base(id) || strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", errors.New("invalid activity ID")
	}
	dir := filepath.Join(p.stateDir, "results", id)
	return dir, os.MkdirAll(dir, 0o700)
}
