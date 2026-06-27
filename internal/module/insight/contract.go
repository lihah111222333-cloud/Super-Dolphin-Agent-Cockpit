// Package insight 消费 Canonical Turn Observation Contract，
// 并把每轮 terminal 事实汇总写入 session insight 持久化端口。
//
// 模块由三段组成：subscriber 只把 terminal 事件放入有界队列；
// flusher 作为 platformrunner.Runner 从 observation.Contract 读取事实并 UPSERT；
// service 只提供 dashboard RPC 使用的只读查询。关闭时 flusher 有界排空队列，
// 避免总线回调直接访问跨模块状态或阻塞关闭路径。
package insight

import (
	"context"
	"encoding/json"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Service 重新导出 contract.InsightService，保持包内和既有下游导入路径稳定。
type Service = contract.InsightService

// Snapshot 是 insight 对外兼容的快照别名，实际 wire 类型由 contract 定义。
type Snapshot = contract.InsightSnapshot

// ApprovalSnapshot 是审批观测快照的兼容别名，dashboard 仍可从 insight 包引用。
type ApprovalSnapshot = contract.InsightApprovalSnapshot

// ErrInvalidLimit 重新导出 contract 层分页错误，避免 RPC 层重复定义 sentinel。
var ErrInvalidLimit = contract.ErrInsightInvalidLimit

// insight 持久化状态常量。
// 这些值会落库并供 UI 聚合使用，新增状态必须同步查询和展示层。
const (
	insightStatusUnknown     = "unknown"
	insightStatusCompleted   = "completed"
	insightStatusInterrupted = "interrupted"
	insightStatusAborted     = "aborted"
	insightStatusFailed      = "failed"
	insightStatusStalled     = "stalled"
)

// insightWriter 是 flusher 写入会话观测结果的最小持久化端口。
type insightWriter interface {
	Upsert(ctx context.Context, params insightUpsertParams) (insightRecord, error)
}

// insightReader 是 dashboard service 读取会话观测结果的最小持久化端口。
type insightReader interface {
	ListByThread(ctx context.Context, threadID string, limit int32) ([]insightRecord, error)
	ListRecent(ctx context.Context, limit int32) ([]insightRecord, error)
	ListObservedApprovalRequests(ctx context.Context, threadID string, limit int32) ([]insightApprovalRow, error)
}

// insightRecord 是单个 turn 的聚合观测结果。
// Success 使用 *bool 区分未知和 false，避免未观测成功状态被误当作失败。
type insightRecord struct {
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

// insightUpsertParams 是 insightWriter.Upsert 的写入参数。
// SkillsSelected 为空时由下层 store 保留既有值，便于采集器分批刷新 token 或终态字段。
type insightUpsertParams struct {
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

// insightApprovalRow 是 ListObservedApprovalRequests 的轻量投影。
// 它只包含已观测到审批请求的 turn，调用方计算均值或分位数时无需再过滤未观测行。
type insightApprovalRow struct {
	ID               int64
	ThreadID         string
	AgentID          string
	LocalTurnID      string
	ProviderTurnID   string
	ApprovalRequests int32
	CreatedAt        time.Time
}

// flushSignal 是 subscriber 推入队列的轻量信号。
// 它携带 flusher 读取 observation 并构造 Insight 行所需的定位字段，避免总线回调做跨模块查询。
type flushSignal struct {
	LocalTurnID string
	ThreadID    string
	AgentID     string
	Provider    string
	Timestamp   time.Time
	Retried     bool
}

// mapTerminalKindToStatus 将 observation.TerminalKind 转为 DB 侧 insight.Status。
// 两层只在空字符串上不一致：这里统一映射为 unknown，避免持久化空状态。
func mapTerminalKindToStatus(k string) string {
	if k == "" {
		return insightStatusUnknown
	}
	return k
}
