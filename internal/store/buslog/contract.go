// Package buslog 提供业务异常日志的只读持久化接口和 UI wire DTO。
package buslog

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义业务异常日志查询边界，调用方不直接依赖 sqlc 生成行。
type Store interface {
	List(ctx context.Context, filter ListFilter) ([]BusExceptionLog, error)
}

// ListFilter 是业务异常日志列表过滤条件，用于按分类、严重级别和关键字收窄。
type ListFilter struct {
	Category string
	Severity string
	Keyword  string
	Limit    int32
}

// BusExceptionLog 是业务异常日志的前端 JSON wire DTO。
type BusExceptionLog struct {
	ID        int64           `json:"id"`
	Ts        time.Time       `json:"ts"`
	Category  string          `json:"category"`
	Severity  string          `json:"severity"`
	Source    string          `json:"source"`
	ToolName  string          `json:"tool_name"`
	Message   string          `json:"message"`
	Traceback string          `json:"traceback"`
	Extra     json.RawMessage `json:"extra"`
}
