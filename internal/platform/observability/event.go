package observability

import "time"

const SchemaVersion = 1

type Status string

const (
	StatusOK             Status = "ok"
	StatusSlow           Status = "slow"
	StatusError          Status = "error"
	StatusPanic          Status = "panic"
	StatusSampled        Status = "sampled"
	StatusDroppedSummary Status = "dropped_summary"
)

type TraceEvent struct {
	SchemaVersion int            `json:"schema_version"`
	Timestamp     time.Time      `json:"ts,omitzero"`
	TraceID       string         `json:"trace_id,omitempty"`
	SpanID        string         `json:"span_id,omitempty"`
	ParentSpanID  string         `json:"parent_span_id,omitempty"`
	Kind          string         `json:"kind,omitempty"`
	Phase         string         `json:"phase,omitempty"`
	Method        string         `json:"method,omitempty"`
	ThreadID      string         `json:"thread_id,omitempty"`
	AgentID       string         `json:"agent_id,omitempty"`
	TurnID        string         `json:"turn_id,omitempty"`
	CallID        string         `json:"call_id,omitempty"`
	ToolName      string         `json:"tool_name,omitempty"`
	ClientKind    string         `json:"client_kind,omitempty"`
	ClientRoute   string         `json:"client_route,omitempty"`
	DurationMS    int64          `json:"duration_ms,omitempty"`
	Status        Status         `json:"status"`
	Error         string         `json:"error,omitempty"`
	Code          CodeAnchor     `json:"code,omitzero"`
	Stack         []StackFrame   `json:"stack,omitempty"`
	Metadata      map[string]any `json:"metadata,omitempty"`
}

type CodeAnchor struct {
	File     string `json:"file,omitempty"`
	Function string `json:"function,omitempty"`
	Line     int    `json:"line,omitempty"`
}

type StackFrame struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Line     int    `json:"line"`
}
