// Package insight 持久化每个 turn 的会话观测指标。
// UPSERT 查询在 SQL 层维护终态优先级和计数非回退约束，避免消费者误把终态降级或回退 token 计数。
package insight

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

// 会话 insight 状态常量。
// 这些值会落库并供 UI 聚合使用，新增状态必须同步查询和展示层。
const (
	StatusUnknown     = "unknown"
	StatusCompleted   = "completed"
	StatusInterrupted = "interrupted"
	StatusAborted     = "aborted"
	StatusFailed      = "failed"
	StatusStalled     = "stalled"
)

// insight 存储哨兵错误。
var (
	ErrNotFound     = errors.New("insight: session insight not found")
	ErrEmptyID      = errors.New("insight: id is required")
	ErrInvalidLimit = errors.New("insight: limit must be between 0 and 500")
)

// Store 是 session insight 的领域访问边界。
// 所有方法只暴露 Insight DTO，sqlc 的 SessionInsight 行类型不能泄露给调用方。
type Store interface {
	Upsert(ctx context.Context, params UpsertParams) (Insight, error)
	GetByLocalTurn(ctx context.Context, threadID, localTurnID string) (Insight, error)
	ListByThread(ctx context.Context, threadID string, limit int32) ([]Insight, error)
	ListRecent(ctx context.Context, limit int32) ([]Insight, error)
	ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]ApprovalRow, error)
	ListObservedTokenTurns(ctx context.Context, threadID string, limit int32) ([]TokenRow, error)
}

// Insight 是单个 turn 的聚合观测结果。
// Success 使用 *bool 区分未知和 false，避免未观测成功状态被误当作失败。
type Insight struct {
	ID                       int64
	ThreadID                 string
	AgentID                  string
	SessionID                string
	Provider                 string
	LocalTurnID              string
	ProviderTurnID           string
	StartedAt                time.Time
	CompletedAt              time.Time
	DurationMS               int32
	Success                  *bool
	Status                   string
	StopReason               string
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
	TokenInput               int32
	TokenOutput              int32
	TokenTotal               int32
	TokenSnapshotObserved    bool
	ContextWindowTokens      int32
	UIProjection             string
	SkillsSelected           json.RawMessage
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// UpsertParams 是 Store.Upsert 的写入参数。
// SkillsSelected 为空时由 SQL 更新语句视为不变，便于采集器分批刷新 token 或终态字段。
type UpsertParams struct {
	ThreadID                 string
	AgentID                  string
	SessionID                string
	Provider                 string
	LocalTurnID              string
	ProviderTurnID           string
	StartedAt                time.Time
	CompletedAt              time.Time
	DurationMS               int32
	Success                  *bool
	Status                   string
	StopReason               string
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
	TokenInput               int32
	TokenOutput              int32
	TokenTotal               int32
	TokenSnapshotObserved    bool
	ContextWindowTokens      int32
	UIProjection             string
	SkillsSelected           json.RawMessage
	CreatedAt                time.Time
	UpdatedAt                time.Time
}

// ApprovalRow 是 ListObservedApprovalRequests 的轻量投影。
// 它只包含已观测到审批请求的 turn，调用方计算均值或分位数时无需再过滤未观测行。
type ApprovalRow struct {
	ID               int64
	ThreadID         string
	AgentID          string
	LocalTurnID      string
	ProviderTurnID   string
	ApprovalRequests int32
	CreatedAt        time.Time
}

// TokenRow 是 ListObservedTokenTurns 的轻量投影。
// 它只承载已采集 token 快照的 turn 摘要，不表示缺失 turn 的 token 值为 0。
type TokenRow struct {
	ID                  int64
	ThreadID            string
	AgentID             string
	LocalTurnID         string
	ProviderTurnID      string
	TokenInput          int32
	TokenOutput         int32
	TokenTotal          int32
	ContextWindowTokens int32
	CreatedAt           time.Time
}
