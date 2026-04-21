// Package feedback persists append-only user/system feedback events tagged
// with the agent_key and prompt_version_id that were active at the moment of
// the event. Aggregations of these rows are the raw signal for "独立优化每个
// agent" (per-agent metrics, prompt A/B comparison).
package feedback

import (
	"context"
	"encoding/json"
	"time"
)

// Store is the write + read surface the feedback module uses.
type Store interface {
	Insert(ctx context.Context, ev Event) (Event, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]Event, error)
	ListByAgentKey(ctx context.Context, agentKey string, limit int32) ([]Event, error)
}

// Event is the domain DTO for an agent_feedback_events row.
//
// EventType is a free-form string but common values (documented for UI/
// analysis consumers, not enforced by the store) are:
//
//	thumbs_up, thumbs_down  — explicit user satisfaction signal
//	retry                   — user re-ran same turn; signal of partial failure
//	edit                    — user edited prompt then re-sent
//	handoff_out             — this thread was superseded by a handoff
//	user_override_route     — user manually pinned agent_key, overriding router
type Event struct {
	ID              int64           `json:"id"`
	ThreadID        string          `json:"thread_id"`
	TurnID          string          `json:"turn_id,omitempty"`
	AgentKey        string          `json:"agent_key,omitempty"`
	PromptVersionID *int64          `json:"prompt_version_id,omitempty"`
	EventType       string          `json:"event_type"`
	Actor           string          `json:"actor,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
	CreatedAt       time.Time       `json:"created_at"`
}
