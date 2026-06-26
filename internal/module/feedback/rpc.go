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

// NewHandlers 注册 feedback JSON-RPC 处理器。
// RPC 层只做参数 wire 适配和响应字段兼容，校验与持久化由 Service.Record 负责。
func NewHandlers(svc Service) platformrpc.HandlerMapResult {
	return platformrpc.HandlerMapResult{Handlers: handler.Map{
		"feedback/record": newRecordHandler(svc),
	}}
}

// newRecordHandler 构造 feedback/record 的严格 RPC handler。
// 返回值同时带 event_type 与 eventType，兼容旧前端和新前端字段读取。
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
