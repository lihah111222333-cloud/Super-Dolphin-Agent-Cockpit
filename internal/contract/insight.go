package contract

import (
	"context"
	"encoding/json"
	"errors"
	"time"
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

const (
	// InsightStatusUnknown is the default status when a turn has no terminal
	// observation yet.
	InsightStatusUnknown = "unknown"
	// InsightStatusCompleted marks a successful completed turn.
	InsightStatusCompleted = "completed"
	// InsightStatusInterrupted marks a user-interrupted turn.
	InsightStatusInterrupted = "interrupted"
	// InsightStatusAborted marks an aborted turn.
	InsightStatusAborted = "aborted"
	// InsightStatusFailed marks a failed turn.
	InsightStatusFailed = "failed"
	// InsightStatusStalled marks a stalled turn.
	InsightStatusStalled = "stalled"
)

var (
	// ErrInsightNotFound reports that no session insight row matched a lookup.
	ErrInsightNotFound = errors.New("insight: session insight not found")
	// ErrInsightEmptyID reports a missing insight identity.
	ErrInsightEmptyID = errors.New("insight: id is required")
)

// InsightStore persists turn-level aggregate metrics.
type InsightStore interface {
	Upsert(ctx context.Context, params InsightUpsertParams) (Insight, error)
	GetByLocalTurn(ctx context.Context, threadID, localTurnID string) (Insight, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]Insight, error)
	ListRecent(ctx context.Context, limit int32) ([]Insight, error)
	ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]InsightApprovalRow, error)
	ListObservedTokenTurns(ctx context.Context, threadID string, limit int32) ([]InsightTokenRow, error)
}

// Insight is the per-turn aggregate stored by the insight persistence port.
type Insight struct {
	ID                       int64
	ThreadID                 string
	AgentID                  string
	SessionID                string
	Provider                 string
	LocalTurnID              string
	ProviderTurnID           string
	StartedAt                time.Time
	CompletedAt              time.Time
	DurationMS               int32
	Success                  *bool
	Status                   string
	StopReason               string
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
	TokenInput               int32
	TokenOutput              int32
	TokenTotal               int32
	TokenSnapshotObserved    bool
	ContextWindowTokens      int32
	UIProjection             string
	SkillsSelected           json.RawMessage
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// InsightUpsertParams drives InsightStore.Upsert.
type InsightUpsertParams struct {
	ThreadID                 string
	AgentID                  string
	SessionID                string
	Provider                 string
	LocalTurnID              string
	ProviderTurnID           string
	StartedAt                time.Time
	CompletedAt              time.Time
	DurationMS               int32
	Success                  *bool
	Status                   string
	StopReason               string
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
	TokenInput               int32
	TokenOutput              int32
	TokenTotal               int32
	TokenSnapshotObserved    bool
	ContextWindowTokens      int32
	UIProjection             string
	SkillsSelected           json.RawMessage
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// InsightApprovalRow is the persistence projection for approval metrics.
type InsightApprovalRow struct {
	ID               int64
	ThreadID         string
	AgentID          string
	LocalTurnID      string
	ProviderTurnID   string
	ApprovalRequests int32
	CreatedAt        time.Time
}

// InsightTokenRow is the persistence projection for token metrics.
type InsightTokenRow struct {
	ID                  int64
	ThreadID            string
	AgentID             string
	LocalTurnID         string
	ProviderTurnID      string
	TokenInput          int32
	TokenOutput         int32
	TokenTotal          int32
	ContextWindowTokens int32
	CreatedAt           time.Time
}

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
