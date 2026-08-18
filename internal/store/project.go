package store

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mwahlig/lazy-xcode/internal/model"
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
		return filepath.Join(root, "lazy-xcode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "state", "lazy-xcode"), nil
}

func cacheRoot() (string, error) {
	if root := os.Getenv("XDG_CACHE_HOME"); root != "" {
		return filepath.Join(root, "lazy-xcode"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".cache", "lazy-xcode"), nil
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:8])
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

func (p *Project) ReadLog(record model.BuildRecord) ([]byte, error) {
	if record.LogPath == "" {
		return nil, nil
	}
	clean := filepath.Clean(record.LogPath)
	logs := filepath.Join(p.stateDir, "logs") + string(os.PathSeparator)
	if !strings.HasPrefix(clean, logs) {
		return nil, errors.New("build log is outside the project state directory")
	}
	data, err := os.ReadFile(clean)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return data, err
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
			history.Builds[i].Error = "lazy-xcode exited before the activity completed"
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
	if len(history) > historyLimit {
		for _, old := range history[historyLimit:] {
			if old.LogPath != "" {
				_ = os.Remove(old.LogPath)
			}
		}
		history = history[:historyLimit]
	}
	if err := writeJSONAtomic(filepath.Join(p.stateDir, "builds.json"), historyFile{Version: 1, Builds: history}); err != nil {
		return nil, err
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
