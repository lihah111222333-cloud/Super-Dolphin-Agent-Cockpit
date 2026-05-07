package contract

import (
	"context"
	"errors"
)

// InsightService is the read-side facade for turn-level metrics.
// Consumed by dashboard RPC handlers; implemented by the insight module.
type InsightService interface {
	ListRecent(ctx context.Context, limit int32) ([]InsightSnapshot, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]InsightSnapshot, error)
	ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]InsightApprovalSnapshot, error)
}

// ErrInsightInvalidLimit is a sentinel for callers that pass a negative limit.
var ErrInsightInvalidLimit = errors.New("insight: limit must be >= 0")

// InsightSnapshot is the read-side projection of a session_insights row.
type InsightSnapshot struct {
	ID                       int64    `json:"id"`
	ThreadID                 string   `json:"thread_id,omitempty"`
	AgentID                  string   `json:"agent_id,omitempty"`
	SessionID                string   `json:"session_id,omitempty"`
	Provider                 string   `json:"provider,omitempty"`
	LocalTurnID              string   `json:"local_turn_id,omitempty"`
	ProviderTurnID           string   `json:"provider_turn_id,omitempty"`
	StartedAt                string   `json:"started_at,omitempty"`
	CompletedAt              string   `json:"completed_at,omitempty"`
	DurationMS               int32    `json:"duration_ms"`
	Success                  *bool    `json:"success,omitempty"`
	Status                   string   `json:"status,omitempty"`
	StopReason               string   `json:"stop_reason,omitempty"`
	ToolCalls                int32    `json:"tool_calls"`
	ToolCallsObserved        bool     `json:"tool_calls_observed"`
	ToolFailures             int32    `json:"tool_failures"`
	ToolFailuresObserved     bool     `json:"tool_failures_observed"`
	ApprovalRequests         int32    `json:"approval_requests"`
	ApprovalRequestsObserved bool     `json:"approval_requests_observed"`
	TokenInput               int32    `json:"token_input"`
	TokenOutput              int32    `json:"token_output"`
	TokenTotal               int32    `json:"token_total"`
	TokenSnapshotObserved    bool     `json:"token_snapshot_observed"`
	ContextWindowTokens      int32    `json:"context_window_tokens"`
	UIProjection             string   `json:"ui_projection,omitempty"`
	SkillsSelected           []string `json:"skills_selected,omitempty"`
	CreatedAt                string   `json:"created_at,omitempty"`
}

// InsightApprovalSnapshot is a slim projection for observed approval metrics.
type InsightApprovalSnapshot struct {
	ID               int64  `json:"id"`
	ThreadID         string `json:"thread_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	LocalTurnID      string `json:"local_turn_id,omitempty"`
	ProviderTurnID   string `json:"provider_turn_id,omitempty"`
	ApprovalRequests int32  `json:"approval_requests"`
	CreatedAt        string `json:"created_at,omitempty"`
}
