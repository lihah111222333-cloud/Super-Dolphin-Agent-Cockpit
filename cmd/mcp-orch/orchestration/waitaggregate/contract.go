// Package waitaggregate 提供存储无关的多目标等待收口器。
package waitaggregate

import (
	"context"
	"errors"
	"reflect"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
)

// Condition 是多目标等待的收口条件。
type Condition string

const (
	// ConditionAny 在任一目标成功时收口。
	ConditionAny Condition = "any"
	// ConditionAll 要求全部目标成功。
	ConditionAll Condition = "all"
	// ConditionQuorum 要求达到 QuorumCount 个成功目标。
	ConditionQuorum Condition = "quorum"
	// ConditionFirstSuccess 是带 typed predicate 的首个成功等待。
	ConditionFirstSuccess Condition = "first_success"
)

// Predicate 是受限的成功判定，不执行任意表达式或命令。
type Predicate struct {
	RequiredEventType       string `json:"required_event_type"`
	RequireNonEmptyMetadata bool   `json:"require_nonempty_metadata"`
}

// Target 是一个固定 caller/scope 的 durable wait 查询目标。
type Target struct {
	Key  string                 `json:"key"`
	Poll accesswait.PollRequest `json:"poll"`
}

// Request 是一次有界的多目标等待请求。
type Request struct {
	Targets               []Target  `json:"targets"`
	Condition             Condition `json:"condition"`
	QuorumCount           int       `json:"quorum_count,omitempty"`
	Predicate             Predicate `json:"predicate,omitempty"`
	DeadlineAt            time.Time `json:"deadline_at"`
	ReturnOnIndeterminate bool      `json:"return_on_indeterminate"`
}

// Validate 检查聚合请求的有界合同和跨目标身份一致性。
func (r Request) Validate() error { return r.ValidateAt(compatibilityClock()) }

// ValidateAt 使用显式观察时间校验 deadline，便于安全时间 owner 做确定性判决。
func (r Request) ValidateAt(now time.Time) error {
	return r.validate(now)
}

// ItemOutcome 是单目标在本次聚合中的判定。
type ItemOutcome string

const (
	// StatusSatisfied 表示聚合条件已满足。
	StatusSatisfied = "satisfied"
	// StatusTimeout 表示本次 bounded wait 到期，句柄仍可续查。
	StatusTimeout = "timeout"
	// StatusAggregateFailure 表示所有目标已定论但 predicate 未满足。
	StatusAggregateFailure = "aggregate_failure"
	// StatusIndeterminate 表示存在 gap、quarantine 或未知状态。
	StatusIndeterminate = "indeterminate"
)

const (
	// OutcomeSuccess 表示目标满足 predicate。
	OutcomeSuccess ItemOutcome = "success"
	// OutcomeDefinitiveFailure 表示目标进入 selector-specific 终态但未成功。
	OutcomeDefinitiveFailure ItemOutcome = "definitive_failure"
	// OutcomePending 表示目标仍可继续等待。
	OutcomePending ItemOutcome = "pending"
	// OutcomeIndeterminate 表示 gap、quarantine 或未知错误，不能当失败。
	OutcomeIndeterminate ItemOutcome = "indeterminate"
)

// Item 是按请求输入顺序返回的单目标快照。
type Item struct {
	TargetKey string                `json:"target_key"`
	Result    accesswait.WaitResult `json:"result"`
	Err       error                 `json:"-"`
	Outcome   ItemOutcome           `json:"outcome"`
}

// Result 是聚合结果；Item 顺序稳定且不会改写 durable wait handle。
type Result struct {
	Condition    Condition `json:"condition"`
	Status       string    `json:"status"`
	Items        []Item    `json:"items"`
	WinnerKey    string    `json:"winner_key,omitempty"`
	SuccessCount int       `json:"success_count"`
	IsError      bool      `json:"is_error"`
}

// Waiter 是 accesswait.Store 的存储无关窄端口。
type Waiter interface {
	Wait(context.Context, accesswait.PollRequest) (accesswait.WaitResult, error)
}

var _ Waiter = (*accesswait.Store)(nil)

// WaitPort 是 Waiter 的兼容名称，保持端口语义而不引入存储依赖。
type WaitPort = Waiter

// Clock 是聚合候选消费的显式安全时间 seam。
//
// 生产接线必须把已认证 security-time owner 的 UTC wall 观察传入
// NewWithClock；它不携带 owner lineage/epoch，也不能单独成为授权事实。
// 该窄函数只用于在聚合请求校验和本地等待上下文之间复用同一次观察。
type Clock = accesswait.Clock

var (
	// ErrInvalidRequest 表示聚合请求违反有界合同。
	ErrInvalidRequest = errors.New("wait aggregate request is invalid")
	// ErrNilWaiter 表示构造器收到空等待端口。
	ErrNilWaiter = errors.New("wait aggregate waiter is nil")
	// ErrNilClock 表示候选构造缺少显式安全时间 seam。
	ErrNilClock = errors.New("wait aggregate security clock is nil")
	// ErrInvalidClock 表示时钟观察不是非零 UTC 时间。
	ErrInvalidClock = errors.New("wait aggregate security clock is invalid")
	// ErrWaiterFailure 表示 waiter 在返回结果前异常退出。
	//
	// 该错误是稳定的、无敏感细节的分类，不携带 waiter panic 的值或栈。
	// 聚合器把它作为 indeterminate item 返回，避免异常退出被误报为 timeout。
	ErrWaiterFailure = errors.New("wait aggregate waiter failed")
)

// Aggregator 执行并发、有界且不落库的多目标等待。
type Aggregator struct {
	waiter Waiter
	clock  Clock
}

// New 创建兼容聚合器；它只为既有内部测试保留 wall-clock fallback。
//
// 新的 candidate 接线必须使用 NewWithClock，显式绑定 security-time
// observation。该兼容构造不会把本地 wall clock 提升为授权或生产事实。
// Deprecated: candidate composition should use NewWithClock.
func New(waiter Waiter) (*Aggregator, error) {
	return newAggregator(waiter, compatibilityClock)
}

// NewWithClock 创建绑定显式安全时间 seam 的聚合候选。
//
// clock 缺失立即失败；不会在运行期回退到 time.Now 或其它隐式时间源。
func NewWithClock(waiter Waiter, clock Clock) (*Aggregator, error) {
	return newAggregator(waiter, clock)
}

func newAggregator(waiter Waiter, clock Clock) (*Aggregator, error) {
	if waiter == nil || isNilWaiter(waiter) {
		return nil, ErrNilWaiter
	}
	if clock == nil {
		return nil, ErrNilClock
	}
	return &Aggregator{waiter: waiter, clock: clock}, nil
}

func isNilWaiter(waiter Waiter) bool {
	value := reflect.ValueOf(waiter)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Ptr, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}
