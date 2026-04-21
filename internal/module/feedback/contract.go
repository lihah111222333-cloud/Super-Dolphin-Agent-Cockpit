// Package feedback exposes feedback-event capture RPCs to the frontend.
// The underlying store lives at internal/store/feedback; this module is the
// thin JRPC-facing layer (validation + wrapping).
package feedback

import "context"

type Service interface {
	Record(ctx context.Context, req RecordRequest) (RecordResult, error)
}

type RecordRequest struct {
	ThreadID        string
	TurnID          string
	AgentKey        string
	PromptVersionID *int64
	EventType       string
	Actor           string
	Payload         []byte
}

type RecordResult struct {
	ID        int64  `json:"id"`
	EventType string `json:"event_type,omitempty"`
	Recorded  bool   `json:"recorded"`
}
