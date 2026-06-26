// Package auditlog 提供审计事件的持久化接口和 JSON wire DTO。
package auditlog

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义审计事件的追加和查询边界，写入前由 store 校验 Extra JSON。
type Store interface {
	List(ctx context.Context, filter ListFilter) ([]AuditEvent, error)
	Insert(ctx context.Context, params InsertParams) error
}

// ListFilter 是审计事件列表过滤条件，用于按事件、动作、操作者和关键字收窄查询。
type ListFilter struct {
	EventType string
	Action    string
	Actor     string
	Keyword   string
	Limit     int32
}

// InsertParams 是追加审计事件的输入，Extra 必须是合法 JSON 片段。
type InsertParams struct {
	EventType string
	Action    string
	Result    string
	Actor     string
	Target    string
	Detail    string
	Level     string
	Extra     json.RawMessage
}

// AuditEvent 是审计日志的前端 JSON wire DTO，时间戳已转换为 time.Time。
type AuditEvent struct {
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	EventType string          `json:"event_type"`
	Action    string          `json:"action"`
	Result    string          `json:"result"`
	Actor     string          `json:"actor"`
	Target    string          `json:"target"`
	Detail    string          `json:"detail"`
	Level     string          `json:"level"`
	Extra     json.RawMessage `json:"extra"`
}
