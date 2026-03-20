package orchestration

import (
	"context"
	"time"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

type Service interface {
	LaunchAgent(ctx context.Context, req LaunchRequest) error
	StopAgent(ctx context.Context, agentID string) error
	SubmitTurn(ctx context.Context, req TurnSubmission) error
	CompleteTurn(ctx context.Context, agentID, turnID string, success bool, errMsg string) error
	Recover(ctx context.Context, agentID string) error
	Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)
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
	AgentID         string
	Name            string
	ParentID        string
	Cwd             string
	PID             int
	State           string
	ThreadID        string
	ActiveTurnID    string
	PendingTurns    int
	Command         []string
	AllowedTriggers []string
	LastError       string
	StartedAt       time.Time
	UpdatedAt       time.Time
	ExitedAt        *time.Time
}
