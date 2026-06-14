package feedback

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

type recordResponse struct {
	ID             int64  `json:"id"`
	EventType      string `json:"event_type"`
	EventTypeCamel string `json:"eventType"`
	Recorded       bool   `json:"recorded"`
}

type recordParams struct {
	ThreadID        string `json:"thread_id"`
	TurnID          string `json:"turn_id,omitempty"`
	AgentKey        string `json:"agent_key,omitempty"`
	PromptVersionID *int64 `json:"prompt_version_id,omitempty"`
	EventType       string `json:"event_type"`
	Actor           string `json:"actor,omitempty"`
	// json.RawMessage: justified -- arbitrary event payload; schema varies per
	// event_type and is forwarded as opaque []byte to the service/store layer.
	Payload json.RawMessage `json:"payload,omitempty"`
}

// NewHandlers 创建处理器。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"feedback/record": newRecordHandler(svc),
	}}
}

func newRecordHandler(svc Service) handler.Func {
	return platformrpc.StrictHandler(func(ctx context.Context, p recordParams) (any, error) {
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
		return recordResponse{
			ID:             result.ID,
			EventType:      result.EventType,
			EventTypeCamel: result.EventType,
			Recorded:       result.Recorded,
		}, nil
	})
}
