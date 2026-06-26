// Package observation 定义 turn observation 的纯 DTO 和读写接口。
// 包放在 dto 层，insight、dashboard、extractor 等消费者可依赖它，而不横向导入 turn 模块实现。
package observation

import "time"

// TerminalKind 标识 turn 的终止类型。
type TerminalKind string

// turn 终止类型常量。
const (
	TerminalUnknown     TerminalKind = ""
	TerminalCompleted   TerminalKind = "completed"
	TerminalStalled     TerminalKind = "stalled"
	TerminalFailed      TerminalKind = "failed"
	TerminalInterrupted TerminalKind = "interrupted"
	TerminalAborted     TerminalKind = "aborted"
)

// Terminal 是一次观测到的 turn 终止事件。
// Success 使用指针，让消费者区分未知和明确失败。
type Terminal struct {
	Kind    TerminalKind
	Success *bool
	Reason  string
}

// TokenSnapshot 是标准化后的单 turn token 观测快照。
// 零值表示本事件未观测到该字段，不应覆盖已有非零值；Projection 用于区分 thread/turn 等投影粒度。
type TokenSnapshot struct {
	Input               int64
	Output              int64
	Total               int64
	ContextWindowTokens int64
	Projection          string
	Observed            bool
}

// DedupeKey 标识一个需要去重的原始或类型化事件。
// 约定 RawEventID、CallID、Key 三者只设置一个，避免多个维度误合并。
type DedupeKey struct {
	RawEventID string
	CallID     string
	Key        string
}

// Counts 是单 turn 的工具和审批活动聚合计数。
// Observed 标志区分 provider 未发出此类事件和已观测但计数为零两种情况。
type Counts struct {
	ToolCalls                int32
	ToolCallsObserved        bool
	ToolFailures             int32
	ToolFailuresObserved     bool
	ApprovalRequests         int32
	ApprovalRequestsObserved bool
}

// Timestamps 记录 turn 首次开始和最近完成时间。
// 对应事件尚未到达时字段允许为零值。
type Timestamps struct {
	StartedAt   time.Time
	CompletedAt time.Time
}

// ObservationReader 是轨迹采集、insight flush 和 extractor 使用的只读接口。
// 它刻意不暴露写入和去重方法，防止消费者修改 observation 状态。
type ObservationReader interface {
	ResolveLocalTurn(providerID string) (localID string, ok bool)
	ResolveProviderTurn(localID string) (providerID string, ok bool)
	LookupCall(callID string) (localTurnID string, ok bool)
	Tokens(localTurnID string) (TokenSnapshot, bool)
	Terminal(localTurnID string) (Terminal, bool)
	SkillsSelected(localTurnID string) []string
	Counts(localTurnID string) (Counts, bool)
	Timestamps(localTurnID string) (Timestamps, bool)
}

// ObservationWriter 是 observation subscriber 和 turn 内部持有的写入接口。
// 下游消费者不应依赖该接口，以免绕过 observation owner。
type ObservationWriter interface {
	MapTurn(localID, providerID string) (ok bool)
	AttributeCall(callID, localTurnID string) (ok bool)
	RecordTokens(localTurnID string, snap TokenSnapshot) TokenSnapshot
	RecordTerminal(localTurnID string, t Terminal) Terminal
	SetSkillsSelected(localTurnID string, slugs []string)
	Dedupe(key DedupeKey) bool
	IncrementToolCalls(localTurnID string) int32
	IncrementToolFailures(localTurnID string) int32
	IncrementApprovalRequests(localTurnID string) int32
	RecordStartedAt(localTurnID string, at time.Time)
	RecordCompletedAt(localTurnID string, at time.Time)
}

// Contract 是 observation owner 写入、消费者读取的统一门面。
// 实现必须保证并发安全，因为事件总线和读取方可能同时访问。
type Contract interface {
	ObservationReader
	ObservationWriter
}
