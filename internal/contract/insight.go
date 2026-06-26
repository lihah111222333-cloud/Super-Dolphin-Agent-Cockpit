package contract

import (
	"context"
	"errors"
)

// InsightService 是 dashboard RPC 读取 turn 级指标的只读门面。
// insight 模块负责查询与投影，调用方只能通过 limit/threadID 读取快照。
type InsightService interface {
	ListRecent(ctx context.Context, limit int32) ([]InsightSnapshot, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]InsightSnapshot, error)
	ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]InsightApprovalSnapshot, error)
}

// ErrInsightInvalidLimit 表示调用方传入了负数 limit。
var ErrInsightInvalidLimit = errors.New("insight: limit must be >= 0")

// InsightSnapshot 是 session_insights 行面向 UI 的只读投影。
// Observed 字段区分“真实观测到 0”和“该指标未采集”，避免 dashboard 误判。
type InsightSnapshot struct {
	ID                       int64    `json:"id"`
	ThreadID                 string   `json:"thread_id,omitempty"`
	AgentID                  string   `json:"agent_id,omitempty"`
	SessionID                string   `json:"session_id,omitempty"`
	Provider                 string   `json:"provider,omitempty"`
	LocalTurnID              string   `json:"local_turn_id,omitempty"`
	ProviderTurnID           string   `json:"provider_turn_id,omitempty"`
	StartedAt                string   `json:"started_at,omitempty"`
	CompletedAt              string   `json:"completed_at,omitempty"`
	DurationMS               int32    `json:"duration_ms"`
	Success                  *bool    `json:"success,omitempty"`
	Status                   string   `json:"status,omitempty"`
	StopReason               string   `json:"stop_reason,omitempty"`
	ToolCalls                int32    `json:"tool_calls"`
	ToolCallsObserved        bool     `json:"tool_calls_observed"`
	ToolFailures             int32    `json:"tool_failures"`
	ToolFailuresObserved     bool     `json:"tool_failures_observed"`
	ApprovalRequests         int32    `json:"approval_requests"`
	ApprovalRequestsObserved bool     `json:"approval_requests_observed"`
	TokenInput               int32    `json:"token_input"`
	TokenOutput              int32    `json:"token_output"`
	TokenTotal               int32    `json:"token_total"`
	TokenSnapshotObserved    bool     `json:"token_snapshot_observed"`
	ContextWindowTokens      int32    `json:"context_window_tokens"`
	UIProjection             string   `json:"ui_projection,omitempty"`
	SkillsSelected           []string `json:"skills_selected,omitempty"`
	CreatedAt                string   `json:"created_at,omitempty"`
}

// InsightApprovalSnapshot 是审批观测指标的轻量只读投影。
// 它只暴露 dashboard 排名和筛选需要的字段，不携带审批详情 payload。
type InsightApprovalSnapshot struct {
	ID               int64  `json:"id"`
	ThreadID         string `json:"thread_id,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	LocalTurnID      string `json:"local_turn_id,omitempty"`
	ProviderTurnID   string `json:"provider_turn_id,omitempty"`
	ApprovalRequests int32  `json:"approval_requests"`
	CreatedAt        string `json:"created_at,omitempty"`
}
