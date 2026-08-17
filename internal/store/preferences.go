package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type Preferences struct {
	mu   sync.Mutex
	path string
	data preferenceData
}

type preferenceData struct {
	Version    int               `json:"version"`
	Containers map[string]string `json:"containers"`
	Schemes    map[string]string `json:"schemes"`
	Simulators map[string]string `json:"simulators"`
}

func NewPreferences() (*Preferences, error) {
	root, err := stateRoot()
	if err != nil {
		return nil, err
	}
	p := &Preferences{path: filepath.Join(root, "preferences.json")}
	p.data = preferenceData{Version: 1, Containers: map[string]string{}, Schemes: map[string]string{}, Simulators: map[string]string{}}
	data, err := os.ReadFile(p.path)
	if err == nil {
		if err := json.Unmarshal(data, &p.data); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	p.ensureMaps()
	return p, nil
}

func (p *Preferences) ensureMaps() {
	if p.data.Containers == nil {
		p.data.Containers = map[string]string{}
	}
	if p.data.Schemes == nil {
		p.data.Schemes = map[string]string{}
	}
	if p.data.Simulators == nil {
		p.data.Simulators = map[string]string{}
	}
}

func (p *Preferences) Container(directory string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.Containers[directory]
}

func (p *Preferences) Scheme(container string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.Schemes[container]
}

func (p *Preferences) Simulator(container, scheme string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.Simulators[container+"\x00"+scheme]
}

func (p *Preferences) SetContainer(directory, container string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.Containers[directory] = container
	return writeJSONAtomic(p.path, p.data)
}

func (p *Preferences) SetScheme(container, scheme string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.Schemes[container] = scheme
	return writeJSONAtomic(p.path, p.data)
}

func (p *Preferences) SetSimulator(container, scheme, simulator string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.Simulators[container+"\x00"+scheme] = simulator
	return writeJSONAtomic(p.path, p.data)
}
