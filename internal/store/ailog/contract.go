// Package ailog 提供 AI 日志查询的持久化接口和统计 DTO。
package ailog

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义 AI 日志查询边界，返回值会补充分类和 HTTP 派生字段供 UI 使用。
type Store interface {
	List(ctx context.Context, filter ListFilter) ([]AILog, error)
	ListByCategory(ctx context.Context, category string, keyword string, limit int32) ([]AILog, error)
	CountByStatus(ctx context.Context) ([]StatusCount, error)
	ListRecent(ctx context.Context, limit int32) ([]AILog, error)
}

// ListFilter 是 AI 日志列表的轻量过滤条件，Limit 由调用方控制窗口大小。
type ListFilter struct {
	Keyword string
	Limit   int32
}

// AILog 是 AI 日志的跨模块 DTO，保留原始日志字段并附带查询侧派生的分类信息。
type AILog struct {
	ID           int64
	Ts           time.Time
	Level        string
	Logger       string
	Message      string
	Raw          string
	Source       string
	Component    string
	AgentID      string
	ThreadID     string
	TraceID      string
	SpanID       string
	ParentSpanID string
	EventType    string
	ToolName     string
	DurationMs   *int32
	Extra        json.RawMessage
	Category     string
	Method       string
	URL          string
	Endpoint     string
	Status       string
	StatusText   string
	Model        string
}

// StatusCount 表示从 AI 日志消息中提取出的 HTTP 状态聚合结果。
type StatusCount struct {
	Status string
	Count  int64
}
