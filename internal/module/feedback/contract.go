// Package feedback 提供用户反馈事件的记录能力，通过 JSON-RPC 接口接收前端事件并持久化。
package feedback

import (
	"context"
	"encoding/json"
	"time"
)

// Service 定义 feedback 模块的反馈事件记录接口。
type Service interface {
	Record(ctx context.Context, req RecordRequest) (RecordResult, error)
}

// Writer 提供 feedback 服务记录事件所需的最小持久化入口。
type Writer interface {
	Insert(ctx context.Context, event Event) (Event, error)
}

// Event 是 feedback 模块拥有的持久化事件投影。
type Event struct {
	ID              int64
	ThreadID        string
	TurnID          string
	AgentKey        string
	PromptVersionID *int64
	EventType       string
	Actor           string
	Payload         json.RawMessage
	CreatedAt       time.Time
}

// RecordRequest 是记录反馈事件的请求参数。
type RecordRequest struct {
	ThreadID        string
	TurnID          string
	AgentKey        string
	PromptVersionID *int64
	EventType       string
	Actor           string
	Payload         []byte
}

// RecordResult 是反馈事件持久化后的最小返回值。
type RecordResult struct {
	ID        int64  `json:"id"`
	EventType string `json:"event_type,omitempty"`
	Recorded  bool   `json:"recorded"`
}
