package turn

import "strings"

// EvaluationVerdict 是轨迹是否进入提炼队列的判断结果，Reason 使用稳定枚举便于指标和审计聚合。
type EvaluationVerdict struct {
	Eligible bool
	Reason   string
}

// Evaluator 判断轨迹是否值得交给 LLM 提炼；实现必须保持纯函数，不读写存储、不访问网络。
type Evaluator interface {
	Evaluate(t Trajectory) EvaluationVerdict
}

// Reason 常量是 evaluator 的稳定拒绝原因枚举。
const (
	ReasonOK                      = "ok"
	ReasonNonCompletedTerminal    = "non_completed_terminal"
	ReasonCompletionMarkedFailure = "completion_marked_failure"
	ReasonToolCallsBelowMin       = "tool_calls_below_min"
	ReasonToolCallsAboveMax       = "tool_calls_above_max"
	ReasonAllToolCallsFailed      = "all_tool_calls_failed"
)

// DefaultEvaluator 按固定顺序执行终态、成功标记、工具数量和失败比例检查；MaxToolCalls=0 表示不设上限。
type DefaultEvaluator struct {
	MinToolCalls int
	MaxToolCalls int
}

// NewDefaultEvaluator 返回默认启发式规则：至少两个工具调用，默认不设置上限。
func NewDefaultEvaluator() *DefaultEvaluator {
	return &DefaultEvaluator{MinToolCalls: 2}
}

// Evaluate 按固定顺序判断轨迹是否值得进入 LLM 提炼队列，并且不修改输入轨迹。
func (e *DefaultEvaluator) Evaluate(t Trajectory) EvaluationVerdict {
	if reason := terminalRejectionReason(t); reason != "" {
		return EvaluationVerdict{Eligible: false, Reason: reason}
	}
	if len(t.ToolCalls) < normalizedMinToolCalls(e.MinToolCalls) {
		return EvaluationVerdict{Eligible: false, Reason: ReasonToolCallsBelowMin}
	}
	if toolCallLimitExceeded(len(t.ToolCalls), e.MaxToolCalls) {
		return EvaluationVerdict{Eligible: false, Reason: ReasonToolCallsAboveMax}
	}
	if allToolCallsFailed(t.ToolCalls) {
		return EvaluationVerdict{Eligible: false, Reason: ReasonAllToolCallsFailed}
	}
	return EvaluationVerdict{Eligible: true, Reason: ReasonOK}
}

// terminalRejectionReason 在非 completed 或显式失败时返回拒绝原因。
func terminalRejectionReason(t Trajectory) string {
	state := strings.ToLower(strings.TrimSpace(t.TerminalState))
	if state != "completed" {
		return ReasonNonCompletedTerminal
	}
	if t.Success != nil && !*t.Success {
		return ReasonCompletionMarkedFailure
	}
	return ""
}

// normalizedMinToolCalls 把负数下限归零，避免错误配置拒绝所有轨迹。
func normalizedMinToolCalls(minTools int) int {
	if minTools < 0 {
		return 0
	}
	return minTools
}

// toolCallLimitExceeded 判断工具调用数量是否超过可选上限。
func toolCallLimitExceeded(count, maxTools int) bool {
	return maxTools > 0 && count > maxTools
}

// allToolCallsFailed 判断是否所有工具调用都失败，空列表不算失败集合。
func allToolCallsFailed(calls []ToolCall) bool {
	if len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		if !call.Failed {
			return false
		}
	}
	return true
}

// 编译期断言确保 DefaultEvaluator 持续满足 Evaluator 接口。
var _ Evaluator = (*DefaultEvaluator)(nil)
