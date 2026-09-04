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
	Physical   bool   `json:"physical,omitempty"`
}

func (s Simulator) Label() string {
	label := s.Name
	if s.OS != "" {
		label += " (" + s.OS + ")"
	}
	return label
}

func (s Simulator) KindLabel() string {
	if s.IsMac() {
		return "Mac"
	}
	if s.Physical {
		return "Device"
	}
	return "Simulator"
}

func (s Simulator) IsMac() bool { return s.Platform == "macOS" }

func (s Simulator) IsSimulator() bool { return !s.Physical && !s.IsMac() }

type Phase string

const (
	PhaseQueued      Phase = "queued"
	PhaseBuilding    Phase = "building"
	PhaseTesting     Phase = "testing"
	PhaseBooting     Phase = "booting"
	PhaseInstalling  Phase = "installing"
	PhaseLaunching   Phase = "launching"
	PhaseSucceeded   Phase = "succeeded"
	PhaseBuildFailed Phase = "build_failed"
	PhaseTestFailed  Phase = "test_failed"
	PhaseRunFailed   Phase = "run_failed"
	PhaseCancelled   Phase = "cancelled"
)

func (p Phase) Active() bool {
	switch p {
	case PhaseQueued, PhaseBuilding, PhaseTesting, PhaseBooting, PhaseInstalling, PhaseLaunching:
		return true
	default:
		return false
	}
}

type Operation string

const (
	OperationBuild Operation = "build"
	OperationTest  Operation = "test"
)

type TestKind string

const (
	TestUnit TestKind = "unit"
	TestUI   TestKind = "ui"
)

type TestTarget struct {
	Name string   `json:"name"`
	Kind TestKind `json:"kind"`
}

type BuildRecord struct {
	ID             string     `json:"id"`
	Container      Container  `json:"container"`
	Scheme         string     `json:"scheme"`
	Simulator      Simulator  `json:"simulator"`
	Phase          Phase      `json:"phase"`
	Operation      Operation  `json:"operation,omitempty"`
	TestScope      string     `json:"testScope,omitempty"`
	TestTargets    []string   `json:"testTargets,omitempty"`
	StartedAt      time.Time  `json:"startedAt"`
	FinishedAt     *time.Time `json:"finishedAt,omitempty"`
	Error          string     `json:"error,omitempty"`
	DerivedDataKey string     `json:"derivedDataKey"`
	LogPath        string     `json:"logPath"`
}

func (r BuildRecord) OperationKind() Operation {
	if r.Operation == OperationTest {
		return OperationTest
	}
	return OperationBuild
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
