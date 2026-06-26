// Package insight 消费 Canonical Turn Observation Contract，
// 并把每轮 terminal 事实汇总写入 internal/store/insight。
//
// 模块由三段组成：subscriber 只把 terminal 事件放入有界队列；
// flusher 作为 platformrunner.Runner 从 observation.Contract 读取事实并 UPSERT；
// service 只提供 dashboard RPC 使用的只读查询。关闭时 flusher 有界排空队列，
// 避免总线回调直接访问跨模块状态或阻塞关闭路径。
package insight

import (
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	insightstore "github.com/anthropic-ai/super-agent-v3/internal/store/insight"
)

// Service 重新导出 contract.InsightService，保持包内和既有下游导入路径稳定。
type Service = contract.InsightService

// Snapshot 是 insight 对外兼容的快照别名，实际 wire 类型由 contract 定义。
type Snapshot = contract.InsightSnapshot

// ApprovalSnapshot 是审批观测快照的兼容别名，dashboard 仍可从 insight 包引用。
type ApprovalSnapshot = contract.InsightApprovalSnapshot

// ErrInvalidLimit 重新导出 contract 层分页错误，避免 RPC 层重复定义 sentinel。
var ErrInvalidLimit = contract.ErrInsightInvalidLimit

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
		return insightstore.StatusUnknown
	}
	return k
}
