// Package feedback 提供用户反馈事件的记录能力，通过 JSON-RPC 接口接收前端事件并持久化。
package feedback

import (
	"context"
	"encoding/json"

	"github.com/creachadair/jrpc2/handler"

	platformrpc "github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
)

// recordResponse 是 feedback/record 的 RPC 响应，同时提供蛇形和驼峰两种 event_type 字段名。
type recordResponse struct {
	ID             int64  `json:"id"`
	EventType      string `json:"event_type"`
	EventTypeCamel string `json:"eventType"`
	Recorded       bool   `json:"recorded"`
}

// recordParams 是 feedback/record 的 RPC 请求参数。
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

// newRecordHandler 创建 feedback/record 的 RPC 处理函数。
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
