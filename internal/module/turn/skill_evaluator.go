package turn

import "strings"

// EvaluationVerdict is the evaluator's heuristic decision over a Trajectory.
// Reason is a stable enum string consumed by metrics / audit; update tests
// whenever a new value is introduced.
type EvaluationVerdict struct {
	Eligible bool
	Reason   string
}

// Evaluator decides whether a Trajectory is worth feeding into the LLM
// distillation queue.
//
// Implementations must be stateless and pure: the same input must produce
// the same verdict (including Reason) on every call. Implementations must
// not read the candidate store, call an LLM, or touch the network.
type Evaluator interface {
	Evaluate(t Trajectory) EvaluationVerdict
}

// Reason enum values. Add a constant and update the table-driven test
// whenever a new reason is introduced.
const (
	ReasonOK                      = "ok"
	ReasonNonCompletedTerminal    = "non_completed_terminal"
	ReasonCompletionMarkedFailure = "completion_marked_failure"
	ReasonToolCallsBelowMin       = "tool_calls_below_min"
	ReasonToolCallsAboveMax       = "tool_calls_above_max"
	ReasonAllToolCallsFailed      = "all_tool_calls_failed"
)

// DefaultEvaluator implements the five ordered heuristic rules documented in
// docs/plans/迁移/p21/P0b/step-3-skill-evaluator.md. MaxToolCalls=0 means no
// upper bound.
type DefaultEvaluator struct {
	MinToolCalls int
	MaxToolCalls int
}

// NewDefaultEvaluator returns the recommended defaults (MinToolCalls=2,
// MaxToolCalls unbounded). Wired via fx into the downstream Step 4
// extractor.
// NewDefaultEvaluator 创建defaultevaluator。
func NewDefaultEvaluator() *DefaultEvaluator {
	return &DefaultEvaluator{MinToolCalls: 2}
}

// Evaluate implements the Evaluator interface. It does not mutate the input
// Trajectory (ToolCalls are not reordered, Cwd is not backfilled, etc.).
// Evaluate 处理evaluate。
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

func normalizedMinToolCalls(minTools int) int {
	if minTools < 0 {
		return 0
	}
	return minTools
}

func toolCallLimitExceeded(count, maxTools int) bool {
	return maxTools > 0 && count > maxTools
}

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

// Compile-time assertion that *DefaultEvaluator implements Evaluator.
var _ Evaluator = (*DefaultEvaluator)(nil)
