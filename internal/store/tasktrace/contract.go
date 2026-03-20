package tasktrace

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Insert(ctx context.Context, trace TaskTrace) (*TaskTrace, error)
	List(ctx context.Context, filter ListFilter) ([]TaskTrace, error)
}

type ListFilter struct {
	Component string
	Since     *time.Time
	Keyword   string
	Limit     int32
}

type TaskTrace struct {
	ID            int64
	TraceID       string
	SpanID        string
	ParentSpanID  string
	SpanName      string
	Component     string
	Status        string
	InputPayload  json.RawMessage
	OutputPayload json.RawMessage
	ErrorText     string
	Metadata      json.RawMessage
	StartedAt     time.Time
	FinishedAt    *time.Time
	DurationMs    int32
}
