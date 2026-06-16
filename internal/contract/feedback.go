package contract

import (
	"context"
	"encoding/json"
	"time"
)

// FeedbackEventStore is the persistence port for append-only feedback events.
type FeedbackEventStore interface {
	// Insert records one feedback event and returns the stored projection.
	Insert(ctx context.Context, ev FeedbackEvent) (FeedbackEvent, error)
	// ListByThread returns recent feedback events for a thread.
	ListByThread(ctx context.Context, threadID string, limit int32) ([]FeedbackEvent, error)
	// ListByAgentKey returns recent feedback events for an agent key.
	ListByAgentKey(ctx context.Context, agentKey string, limit int32) ([]FeedbackEvent, error)
}

// FeedbackEvent is the cross-layer projection of an agent feedback event.
type FeedbackEvent struct {
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
