package model

import "time"

type ContainerKind string

const (
	Workspace ContainerKind = "workspace"
	Project   ContainerKind = "project"
)

type Container struct {
	Kind ContainerKind `json:"kind"`
	Name string        `json:"name"`
	Path string        `json:"path"`
}

type Simulator struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	OS         string `json:"os"`
	Platform   string `json:"platform"`
	State      string `json:"state"`
	DeviceType string `json:"deviceType,omitempty"`
}

func (s Simulator) Label() string {
	label := s.Name
	if s.OS != "" {
		label += " (" + s.OS + ")"
	}
	return label
}

type Phase string

const (
	PhaseQueued      Phase = "queued"
	PhaseBuilding    Phase = "building"
	PhaseBooting     Phase = "booting"
	PhaseInstalling  Phase = "installing"
	PhaseLaunching   Phase = "launching"
	PhaseSucceeded   Phase = "succeeded"
	PhaseBuildFailed Phase = "build_failed"
	PhaseRunFailed   Phase = "run_failed"
	PhaseCancelled   Phase = "cancelled"
)

func (p Phase) Active() bool {
	switch p {
	case PhaseQueued, PhaseBuilding, PhaseBooting, PhaseInstalling, PhaseLaunching:
		return true
	default:
		return false
	}
}

type BuildRecord struct {
	ID             string     `json:"id"`
	Container      Container  `json:"container"`
	Scheme         string     `json:"scheme"`
	Simulator      Simulator  `json:"simulator"`
	Phase          Phase      `json:"phase"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	Error          string     `json:"error,omitempty"`
	DerivedDataKey string     `json:"derivedDataKey"`
	LogPath        string     `json:"logPath"`
}

func (r BuildRecord) Duration(now time.Time) time.Duration {
	end := now
	if r.FinishedAt != nil {
		end = *r.FinishedAt
	}
	return end.Sub(r.StartedAt)
}

type Product struct {
	AppPath  string
	BundleID string
}
