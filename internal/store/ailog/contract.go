package ailog

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]AILog, error)
}

type ListFilter struct {
	Keyword string
	Limit   int32
}

type AILog struct {
	ID         int64
	Ts         time.Time
	Level      string
	Logger     string
	Message    string
	Raw        string
	Source     string
	Component  string
	AgentID    string
	ThreadID   string
	TraceID    string
	EventType  string
	ToolName   string
	DurationMs *int32
	Extra      json.RawMessage
}
