// Package feedback 持久化用户和系统反馈事件。
// 事件会记录触发时的 agent_key 和 prompt_version_id，供后续按 agent 或 prompt 版本做质量分析。
package feedback

import (
	"context"
	"encoding/json"
	"time"
)

// Store 是 feedback 模块的读写边界。
// 写入保持 append-only，列表接口只按线程或 agent_key 投影历史事件。
type Store interface {
	Insert(ctx context.Context, ev Event) (Event, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]Event, error)
	ListByAgentKey(ctx context.Context, agentKey string, limit int32) ([]Event, error)
}

// Event 是 agent_feedback_events 的跨模块 DTO。
// EventType 允许自由扩展，但常见值需保持 UI 和分析任务可识别：
//
//	thumbs_up, thumbs_down  — 用户显式满意度信号
//	retry                   — 用户重跑同一 turn，表示前次结果不完整
//	edit                    — 用户编辑 prompt 后重新发送
//	handoff_out             — 当前 thread 被 handoff 取代
//	user_override_route     — 用户手动指定 agent_key，覆盖自动路由
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
