package observability

import "time"

// SchemaVersion 标记 TraceEvent 的 JSON 结构版本，写入和读取侧按它做兼容判断。
const SchemaVersion = 1

// Status 描述一次 trace 事件的结果状态。
type Status string

const (
	// trace 事件状态常量，既用于采样判断也用于诊断摘要分类。
	StatusOK             Status = "ok"
	StatusSlow           Status = "slow"
	StatusError          Status = "error"
	StatusPanic          Status = "panic"
	StatusSampled        Status = "sampled"
	StatusDroppedSummary Status = "dropped_summary"
)

// Metadata 是 trace 事件携带的扩展字段，写入前会被 sanitizer 限制大小和敏感信息。
type Metadata = map[string]any

// TraceEvent 是内存索引和 JSONL sink 共享的 trace 事件形状。
type TraceEvent struct {
	SchemaVersion int          `json:"schema_version"`
	Timestamp     time.Time    `json:"ts,omitzero"`
	TraceID       string       `json:"trace_id,omitempty"`
	SpanID        string       `json:"span_id,omitempty"`
	ParentSpanID  string       `json:"parent_span_id,omitempty"`
	Kind          string       `json:"kind,omitempty"`
	Phase         string       `json:"phase,omitempty"`
	Method        string       `json:"method,omitempty"`
	ThreadID      string       `json:"thread_id,omitempty"`
	AgentID       string       `json:"agent_id,omitempty"`
	TurnID        string       `json:"turn_id,omitempty"`
	CallID        string       `json:"call_id,omitempty"`
	ToolName      string       `json:"tool_name,omitempty"`
	ClientKind    string       `json:"client_kind,omitempty"`
	ClientRoute   string       `json:"client_route,omitempty"`
	DurationMS    int64        `json:"duration_ms,omitempty"`
	Status        Status       `json:"status"`
	Error         string       `json:"error,omitempty"`
	Code          CodeAnchor   `json:"code,omitzero"`
	Stack         []StackFrame `json:"stack,omitempty"`
	Metadata      Metadata     `json:"metadata,omitempty"`
}

// CodeAnchor 描述事件关联的源码位置，路径在诊断输出时会按 workspace 规则脱敏或相对化。
type CodeAnchor struct {
	File     string `json:"file,omitempty"`
	Function string `json:"function,omitempty"`
	Line     int    `json:"line,omitempty"`
}

// StackFrame 描述捕获到的一帧调用栈，诊断输出会按配置限制帧数和路径内容。
type StackFrame struct {
	File     string `json:"file"`
	Function string `json:"function"`
	Line     int    `json:"line"`
}
