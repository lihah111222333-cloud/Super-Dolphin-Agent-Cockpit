package dashboard

import (
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// AgentSnapshot 复用 orchestration 的 agent 快照 wire 结构。
type AgentSnapshot = contract.AgentSnapshot

// AgentOverview 是列表页使用的轻量 agent 快照别名，保持与 AgentSnapshot JSON 兼容。
type AgentOverview = AgentSnapshot

// Dashboard 是 dashboard 首页的聚合 wire 结构。
// Uptime 是本进程计算值，不直接来自 store。
type Dashboard struct {
	Agents     []AgentOverview `json:"agents"`
	System     SystemInfo      `json:"system"`
	TokenUsage TokenUsage      `json:"token_usage"`
	Uptime     time.Duration   `json:"uptime"`
}

// AgentDetail 包含单个 agent 的完整详情，供前端 agent 详情页使用。
type AgentDetail struct {
	AgentID     string        `json:"agent_id,omitempty"`
	Name        string        `json:"name,omitempty"`
	Snapshot    AgentSnapshot `json:"snapshot"`
	ThreadID    string        `json:"thread_id,omitempty"`
	Status      string        `json:"status,omitempty"`
	TurnHistory []TurnRef     `json:"turn_history"`
	LastReport  string        `json:"last_report"`
}

// DashboardDAG 在 DAGSummary 基础上附加最新 run 和 final output 标记。
type DashboardDAG struct {
	contract.DAGSummary
	LatestRun      *contract.Run `json:"latest_run,omitempty"`
	HasFinalOutput bool          `json:"hasFinalOutput"`
}

// FinalOutputRef 是 sharedfile 保留检查使用的最终输出引用。
// Path 是清理候选匹配主键，其余字段只服务 UI 回溯来源 run 和节点，不参与权限判断。
type FinalOutputRef struct {
	Path          string `json:"path"`
	RunKey        string `json:"runKey,omitempty"`
	DagKey        string `json:"dagKey,omitempty"`
	SourceNodeKey string `json:"sourceNodeKey,omitempty"`
	Kind          string `json:"kind,omitempty"`
}

// TokenUsage 记录一次 dashboard 快照的 token 消耗统计。
// 当前字段保留给前端兼容，未采集到指标时保持 omitempty。
type TokenUsage struct {
	InputTokens         int `json:"input_tokens,omitempty"`
	OutputTokens        int `json:"output_tokens,omitempty"`
	TotalTokens         int `json:"total_tokens,omitempty"`
	ContextWindowTokens int `json:"context_window_tokens,omitempty"`
}

// SystemInfo 是 dashboard 首页展示的进程快照。
// 构建信息来自编译变量，runtime/内存数据在请求时采样，因此不应被当成持久化状态。
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

// TurnRef 指向一个 agent turn 的轻量引用，用于历史展示。
type TurnRef struct {
	TurnID    string    `json:"turn_id"`
	ThreadID  string    `json:"thread_id,omitempty"`
	AgentID   string    `json:"agent_id,omitempty"`
	Status    string    `json:"status,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// LogFilter 指定日志查询的统一过滤条件。
// 空字段表示不过滤，Limit 在 service 入口统一 clamp。
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

// LogEntry 是跨 system 和 AI 日志源统一格式化的 wire 条目。
// Source 标记来源，Extra 保留原始 JSON 供前端展开详情。
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
