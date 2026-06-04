package rpc

import (
	"context"
	"time"
)

type TraceStatus string

const (
	TraceStatusOK    TraceStatus = "ok"
	TraceStatusSlow  TraceStatus = "slow"
	TraceStatusError TraceStatus = "error"
)

type TraceCodeAnchor struct {
	File     string
	Function string
	Line     int
}

type TraceRecord struct {
	Timestamp    time.Time
	TraceID      string
	SpanID       string
	ParentSpanID string
	Kind         string
	Phase        string
	Method       string
	DurationMS   int64
	Status       TraceStatus
	Error        string
	Code         TraceCodeAnchor
	Metadata     map[string]any
}

type TraceRecorder interface {
	Enabled() bool
	RecordTrace(context.Context, TraceRecord) error
}
