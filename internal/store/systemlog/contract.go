package systemlog

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]SystemLog, error)
	Insert(ctx context.Context, params InsertParams) error
}

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
