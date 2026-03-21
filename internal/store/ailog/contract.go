package ailog

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	List(ctx context.Context, filter ListFilter) ([]AILog, error)
	ListByCategory(ctx context.Context, category string, limit int32) ([]AILog, error)
	CountByStatus(ctx context.Context) ([]StatusCount, error)
	ListRecent(ctx context.Context, limit int32) ([]AILog, error)
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
	Category   string
	Method     string
	URL        string
	Endpoint   string
	Status     string
	StatusText string
	Model      string
}

type StatusCount struct {
	Status string
	Count  int64
}
