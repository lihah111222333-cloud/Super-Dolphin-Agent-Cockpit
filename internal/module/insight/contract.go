// Package insight consumes the Canonical Turn Observation Contract and
// persists per-turn aggregate metrics through internal/store/insight.
//
// The module wires together three pieces:
//   - subscriber (bus.ResilientSubscribe → queue): terminal events push a
//     small flush signal onto a bounded channel; callbacks do zero work
//     beyond enqueueing.
//   - flusher (platformrunner.Runner): drains the queue, reads facts from
//     observation.Contract, and UPSERTs into insight.Store. Runs until
//     ctx cancels; shutdown is a bounded drain (5s) per the P3 plan.
//   - service: read-side API consumed by dashboard RPC handlers from the
//     persisted rows.
package insight

import (
	"context"
	"errors"
	"time"

	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
)

// Service is the read-side facade consumed by dashboard-owned RPC handlers.
type Service interface {
	ListRecent(ctx context.Context, limit int32) ([]Snapshot, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]Snapshot, error)
	ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalSnapshot, error)
}

// Sentinel errors.
var (
	ErrInvalidLimit = errors.New("insight: limit must be >= 0")
)

// Snapshot is the read-side projection of a session_insights row. Time
// fields are RFC3339 strings so JSON consumers never have to deal with
// pgtype.Timestamptz. Success is *bool to preserve the unknown / true /
// false three-state from the underlying schema.
type Snapshot struct {
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

// ApprovalSnapshot is a slim projection for observed approval metrics.
type ApprovalSnapshot struct {
	ID               int64  `json:"id"`
	ThreadID         string `json:"thread_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	LocalTurnID      string `json:"local_turn_id,omitempty"`
	ProviderTurnID   string `json:"provider_turn_id,omitempty"`
	ApprovalRequests int32  `json:"approval_requests"`
	CreatedAt        string `json:"created_at,omitempty"`
}

// flushSignal is what the subscriber pushes onto the queue. Everything
// the flusher needs to read observation + build the Insight row is
// carried on the signal so the flusher never blocks on cross-module
// lookups.
type flushSignal struct {
	LocalTurnID string
	ThreadID    string
	AgentID     string
	Provider    string
	Timestamp   time.Time
	Retried     bool
}

// mapTerminalKindToStatus translates the observation.TerminalKind string
// into the insight.Status string expected by the DB. The two layers
// agree on all values except empty-string "" → "unknown"; this helper
// is the one place that boundary is crossed.
func mapTerminalKindToStatus(k string) string {
	if k == "" {
		return insightstore.StatusUnknown
	}
	return k
}
