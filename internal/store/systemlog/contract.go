package systemlog

import (
	"context"
	"encoding/json"
	"time"
)

// Store 定义 system_logs 的写入和检索边界。
// 日志生产者只通过 Insert 落库，诊断视图通过 List 使用结构化过滤条件。
type Store interface {
	List(ctx context.Context, filter ListFilter) ([]SystemLog, error)
	Insert(ctx context.Context, params InsertParams) error
}

// ListFilter 描述 system_logs 查询支持的结构化过滤条件。
// trace/span/tool 字段用于串联观测链路，Keyword 保留给全文检索入口。
type ListFilter struct {
	Level        string
	Logger       string
	Source       string
	Component    string
	AgentID      string
	ThreadID     string
	TraceID      string
	SpanID       string
	ParentSpanID string
	EventType    string
	ToolName     string
	Keyword      string
	Limit        int32
}

// InsertParams 是写入一条 system log 时的归一化字段集合。
// Extra 保留结构化 JSON，DurationMs 为指针以区分未记录和零耗时。
type InsertParams struct {
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
}

// SystemLog 是从 system_logs 表读取出的诊断日志 DTO。
// 字段保持与 InsertParams 对齐，并补充数据库生成的 ID 和时间戳。
type SystemLog struct {
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
}
