package runtime

import (
	"context"

	"go-backend/internal/store/repo"
)

type Engine = repo.RuntimeEngine

const (
	EngineGost = repo.RuntimeEngineGost
	EngineDash = repo.RuntimeEngineDash
)

type SwitchStatus = repo.RuntimeSwitchStatus

const (
	RuntimeSwitchStatusIdle      = repo.RuntimeSwitchStatusIdle
	RuntimeSwitchStatusSwitching = repo.RuntimeSwitchStatusSwitching
	RuntimeSwitchStatusFailed    = repo.RuntimeSwitchStatusFailed
)

type SwitchState = repo.RuntimeSwitchState

type ProgressState string

const (
	ProgressStatePending   ProgressState = "pending"
	ProgressStateRunning   ProgressState = "running"
	ProgressStateSucceeded ProgressState = "succeeded"
	ProgressStateFailed    ProgressState = "failed"
)

type NodeRuntimeProgress struct {
	NodeID   int64
	State    ProgressState
	Message  string
	Engine   Engine
	Complete bool
}

type RebuildRuntimeProgress struct {
	State    ProgressState
	Message  string
	Warnings []string
	Engine   Engine
	Complete bool
}

type SwitchProgress struct {
	Engine     Engine
	Status     SwitchStatus
	Generation int64
	Phase      string
	Message    string
	Nodes      []NodeRuntimeProgress
	Rebuild    RebuildRuntimeProgress
}

type NodeRuntimeStatus struct {
	NodeID   int64
	Engine   Engine
	Ready    bool
	Message  string
	Progress ProgressState
}

type ServiceCheckRequest struct {
	MonitorID  int64
	Type       string
	Target     string
	TimeoutSec int
}

type ServiceCheckResult struct {
	Success      bool
	LatencyMs    float64
	StatusCode   int
	ErrorMessage string
}

type RuntimeClient interface {
	EnsureNodeRuntime(ctx context.Context, node repo.Node) (NodeRuntimeProgress, error)
	RebuildAllRuntime(ctx context.Context) (RebuildRuntimeProgress, error)
	GetNodeRuntimeStatus(ctx context.Context, node repo.Node) (NodeRuntimeStatus, error)
	PauseServices(ctx context.Context, node repo.Node, services []string) error
	ResumeServices(ctx context.Context, node repo.Node, services []string) error
	CheckService(ctx context.Context, node repo.Node, req ServiceCheckRequest) (ServiceCheckResult, error)
}
