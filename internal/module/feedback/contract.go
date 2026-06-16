// Package feedback exposes feedback-event capture RPCs to the frontend.
// This module is the thin JRPC-facing layer for validation and wrapping.
package feedback

import "context"

// Service records frontend or system feedback events.
type Service interface {
	Record(ctx context.Context, req RecordRequest) (RecordResult, error)
}

// RecordRequest carries one feedback event and the optional runtime context
// needed to correlate it with a thread, turn, agent, or prompt version.
type RecordRequest struct {
	ThreadID        string
	TurnID          string
	AgentKey        string
	PromptVersionID *int64
	EventType       string
	Actor           string
	Payload         []byte
}

// RecordResult reports the persisted feedback event identity.
type RecordResult struct {
	ID        int64  `json:"id"`
	EventType string `json:"event_type,omitempty"`
	Recorded  bool   `json:"recorded"`
}
