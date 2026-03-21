package dashboard

import (
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type AgentSnapshot = contract.AgentSnapshot
type AgentOverview = AgentSnapshot

type Dashboard struct {
	Agents     []AgentOverview `json:"agents"`
	System     SystemInfo      `json:"system"`
	TokenUsage TokenUsage      `json:"token_usage"`
	Uptime     time.Duration   `json:"uptime"`
}

type AgentDetail struct {
	AgentID     string        `json:"agent_id,omitempty"`
	Name        string        `json:"name,omitempty"`
	Snapshot    AgentSnapshot `json:"snapshot"`
	ThreadID    string        `json:"thread_id,omitempty"`
	Status      string        `json:"status,omitempty"`
	TurnHistory []TurnRef     `json:"turn_history"`
	LastReport  string        `json:"last_report"`
}

type TokenUsage struct {
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	ContextWindowTokens int `json:"context_window_tokens,omitempty"`
}

type SystemInfo struct {
	StartedAt        time.Time `json:"started_at"`
	BuildVersion     string    `json:"build_version"`
	BuildCommit      string    `json:"build_commit"`
	BuildTime        string    `json:"build_time,omitempty"`
	Dirty            bool      `json:"dirty,omitempty"`
	GoVersion        string    `json:"go_version"`
	Runtime          string    `json:"runtime"`
	NumCPU           int       `json:"num_cpu"`
	NumGoroutine     int       `json:"num_goroutine"`
	MemoryAllocBytes uint64    `json:"memory_alloc_bytes"`
	MemorySysBytes   uint64    `json:"memory_sys_bytes"`
	AgentCount       int       `json:"agent_count"`
}

type TurnRef struct {
	TurnID    string    `json:"turn_id"`
	ThreadID  string    `json:"thread_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	Status    string    `json:"status,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

type LogFilter struct {
	Source    string `json:"source,omitempty"`
	Keyword   string `json:"keyword,omitempty"`
	Level     string `json:"level,omitempty"`
	Logger    string `json:"logger,omitempty"`
	Component string `json:"component,omitempty"`
	AgentID   string `json:"agent_id,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
	EventType string `json:"event_type,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`
	Limit     int    `json:"limit,omitempty"`
}

type LogEntry struct {
	Source     string          `json:"source"`
	ID         int64           `json:"id"`
	Timestamp  time.Time       `json:"timestamp"`
	Level      string          `json:"level,omitempty"`
	Logger     string          `json:"logger,omitempty"`
	Message    string          `json:"message,omitempty"`
	Raw        string          `json:"raw,omitempty"`
	Component  string          `json:"component,omitempty"`
	AgentID    string          `json:"agent_id,omitempty"`
	ThreadID   string          `json:"thread_id,omitempty"`
	TraceID    string          `json:"trace_id,omitempty"`
	EventType  string          `json:"event_type,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	DurationMs *int32          `json:"duration_ms,omitempty"`
	Extra      json.RawMessage `json:"extra,omitempty"`
}
