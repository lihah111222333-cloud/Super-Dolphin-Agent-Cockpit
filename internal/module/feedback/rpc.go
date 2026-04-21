package feedback

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type recordParams struct {
	ThreadID        string          `json:"thread_id"`
	TurnID          string          `json:"turn_id,omitempty"`
	AgentKey        string          `json:"agent_key,omitempty"`
	PromptVersionID *int64          `json:"prompt_version_id,omitempty"`
	EventType       string          `json:"event_type"`
	Actor           string          `json:"actor,omitempty"`
	Payload         json.RawMessage `json:"payload,omitempty"`
}

func NewHandlers(svc Service) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"feedback/record": newRecordHandler(svc),
	}}
}

func newRecordHandler(svc Service) handler.Func {
	return rpc.StrictHandler(func(ctx context.Context, p recordParams) (any, error) {
		result, err := svc.Record(ctx, RecordRequest{
			ThreadID:        p.ThreadID,
			TurnID:          p.TurnID,
			AgentKey:        p.AgentKey,
			PromptVersionID: p.PromptVersionID,
			EventType:       p.EventType,
			Actor:           p.Actor,
			Payload:         p.Payload,
		})
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"id":         result.ID,
			"event_type": result.EventType,
			"eventType":  result.EventType,
			"recorded":   result.Recorded,
		}, nil
	})
}
