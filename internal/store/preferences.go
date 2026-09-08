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

const preferencesVersion = 2

type preferenceData struct {
	Version          int               `json:"version"`
	Containers       map[string]string `json:"containers"`
	Schemes          map[string]string `json:"schemes"`
	Simulators       map[string]string `json:"simulators"`
	CloudProducts    map[string]string `json:"cloudProducts,omitempty"`
	CloudWorkflows   map[string]string `json:"cloudWorkflows,omitempty"`
	CoverageDisabled map[string]bool   `json:"coverageDisabled,omitempty"`
}

func NewPreferences() (*Preferences, error) {
	root, err := stateRoot()
	if err != nil {
		return nil, err
	}
	p := &Preferences{path: filepath.Join(root, "preferences.json")}
	p.data = preferenceData{Version: preferencesVersion}
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
	if p.data.CoverageDisabled == nil {
		p.data.CoverageDisabled = map[string]bool{}
	}
	if p.data.Containers == nil {
		p.data.Containers = map[string]string{}
	}
	if p.data.Schemes == nil {
		p.data.Schemes = map[string]string{}
	}
	if p.data.Simulators == nil {
		p.data.Simulators = map[string]string{}
	}
	if p.data.CloudProducts == nil {
		p.data.CloudProducts = map[string]string{}
	}
	if p.data.CloudWorkflows == nil {
		p.data.CloudWorkflows = map[string]string{}
	}
	if p.data.Version < preferencesVersion {
		p.data.Version = preferencesVersion
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

// CloudProduct returns the remembered Xcode Cloud product ID for a container.
func (p *Preferences) CloudProduct(container string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.CloudProducts[container]
}

// CloudWorkflow returns the remembered Xcode Cloud workflow filter for a
// container. An empty value means all workflows.
func (p *Preferences) CloudWorkflow(container string) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.CloudWorkflows[container]
}

func (p *Preferences) SetCloudProduct(container, product string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if product == "" {
		delete(p.data.CloudProducts, container)
	} else {
		p.data.CloudProducts[container] = product
	}
	return writeJSONAtomic(p.path, p.data)
}

func (p *Preferences) SetCloudWorkflow(container, workflow string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if workflow == "" {
		delete(p.data.CloudWorkflows, container)
	} else {
		p.data.CloudWorkflows[container] = workflow
	}
	return writeJSONAtomic(p.path, p.data)
}

func (p *Preferences) CoverageDisabled(container string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.data.CoverageDisabled[container]
}
func (p *Preferences) SetCoverageDisabled(container string, disabled bool) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.data.CoverageDisabled[container] = disabled
	return writeJSONAtomic(p.path, p.data)
}
