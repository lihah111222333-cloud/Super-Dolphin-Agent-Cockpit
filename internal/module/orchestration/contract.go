package orchestration

import (
	"context"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

type Service interface {
	LaunchAgent(ctx context.Context, req LaunchRequest) error
	ListAgents(ctx context.Context) ([]AgentSnapshot, error)
	StopAgent(ctx context.Context, agentID string) error
	SubmitTurn(ctx context.Context, req TurnSubmission) error
	CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error
	Recover(ctx context.Context, agentID string) error
	Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)
	SetReport(ctx context.Context, agentID, report string) error
}

// TODO(P5-R5): 以下方法属于 orchestration service 层，不应停留在 rpc glue。
// CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGResult, error)
// GetDAG(ctx context.Context, dagKey string) (DAGDetail, error)
// ListDAGs(ctx context.Context) ([]DAGSummary, error)
// UpdateNode(ctx context.Context, req UpdateNodeRequest) error

type SessionCleaner interface {
	RemoveSession(agentID string)
}

type TurnSubmission = turndto.TurnSubmission

type LaunchRequest struct {
	AgentID  string
	Name     string
	ParentID string
	Cwd      string
	Command  []string
	Env      []string
}

type AgentSnapshot struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	ParentID   string `json:"parent_id,omitempty"`
	Port       int    `json:"port"`
	ThreadID   string `json:"thread_id"`
	Cwd        string `json:"cwd"`
	State      string `json:"state"`
	Provider   string `json:"provider,omitempty"`
	LastReport string `json:"last_report,omitempty"`
}
