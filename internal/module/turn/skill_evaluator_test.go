package turn

import (
	"testing"
)

func boolPtr(v bool) *bool { return &v }

type evaluatorCase struct {
	name     string
	eval     DefaultEvaluator
	traj     Trajectory
	eligible bool
	reason   string
}

func TestEvaluator_TableDriven(t *testing.T) {
	for _, c := range evaluatorTableCases() {
		c := c
		t.Run(c.name, func(t *testing.T) {
			got := c.eval.Evaluate(c.traj)
			if got.Eligible != c.eligible || got.Reason != c.reason {
				t.Fatalf("Evaluate() = (%v, %q), want (%v, %q)", got.Eligible, got.Reason, c.eligible, c.reason)
			}
		})
	}
}

func evaluatorTableCases() []evaluatorCase {
	cases := evaluatorRuleOneCases()
	cases = append(cases, evaluatorRuleTwoThreeCases()...)
	cases = append(cases, evaluatorRuleFourFiveCases()...)
	return cases
}

func evaluatorRuleOneCases() []evaluatorCase {
	return []evaluatorCase{
		{
			name: "happy path: completed + 2 successful tools",
			eval: DefaultEvaluator{MinToolCalls: 2},
			traj: Trajectory{
				TerminalState: "completed",
				Success:       boolPtr(true),
				ToolCalls: []ToolCall{
					{CallID: "a", Failed: false},
					{CallID: "b", Failed: false},
				},
			},
			eligible: true,
			reason:   ReasonOK,
		},
		{
			name: "rule 1: interrupted blocks even with all good fields",
			eval: DefaultEvaluator{MinToolCalls: 2},
			traj: Trajectory{
				TerminalState: "interrupted",
				Success:       boolPtr(true),
				ToolCalls:     []ToolCall{{Failed: false}, {Failed: false}},
			},
			eligible: false,
			reason:   ReasonNonCompletedTerminal,
		},
		{
			name:     "rule 1: aborted blocks",
			eval:     DefaultEvaluator{MinToolCalls: 2},
			traj:     Trajectory{TerminalState: "aborted", ToolCalls: []ToolCall{{}, {}}},
			eligible: false,
			reason:   ReasonNonCompletedTerminal,
		},
		{
			name:     "rule 1: failed blocks",
			eval:     DefaultEvaluator{MinToolCalls: 2},
			traj:     Trajectory{TerminalState: "failed", ToolCalls: []ToolCall{{}, {}}},
			eligible: false,
			reason:   ReasonNonCompletedTerminal,
		},
		{
			name:     "rule 1: empty terminal blocks",
			eval:     DefaultEvaluator{MinToolCalls: 2},
			traj:     Trajectory{ToolCalls: []ToolCall{{}, {}}},
			eligible: false,
			reason:   ReasonNonCompletedTerminal,
		},
		{
			name:     "rule 1: case insensitive Completed",
			eval:     DefaultEvaluator{MinToolCalls: 0},
			traj:     Trajectory{TerminalState: "Completed", Success: boolPtr(true)},
			eligible: true,
			reason:   ReasonOK,
		},
	}
}

func evaluatorRuleTwoThreeCases() []evaluatorCase {
	return []evaluatorCase{
		{
			name:     "rule 2: explicit Success=false rejects",
			eval:     DefaultEvaluator{MinToolCalls: 0},
			traj:     Trajectory{TerminalState: "completed", Success: boolPtr(false)},
			eligible: false,
			reason:   ReasonCompletionMarkedFailure,
		},
		{
			name:     "rule 2: nil Success treated as eligible (when other rules pass)",
			eval:     DefaultEvaluator{MinToolCalls: 0},
			traj:     Trajectory{TerminalState: "completed", Success: nil},
			eligible: true,
			reason:   ReasonOK,
		},
		{
			name: "rule 3: below MinToolCalls",
			eval: DefaultEvaluator{MinToolCalls: 3},
			traj: Trajectory{
				TerminalState: "completed",
				Success:       boolPtr(true),
				ToolCalls:     []ToolCall{{Failed: false}, {Failed: false}},
			},
			eligible: false,
			reason:   ReasonToolCallsBelowMin,
		},
		{
			name:     "rule 3: zero tools with MinToolCalls=0 is OK",
			eval:     DefaultEvaluator{MinToolCalls: 0},
			traj:     Trajectory{TerminalState: "completed", Success: boolPtr(true)},
			eligible: true,
			reason:   ReasonOK,
		},
	}
}

func evaluatorRuleFourFiveCases() []evaluatorCase {
	return []evaluatorCase{
		{
			name: "rule 4: above MaxToolCalls",
			eval: DefaultEvaluator{MinToolCalls: 0, MaxToolCalls: 2},
			traj: Trajectory{
				TerminalState: "completed",
				Success:       boolPtr(true),
				ToolCalls:     []ToolCall{{}, {}, {}},
			},
			eligible: false,
			reason:   ReasonToolCallsAboveMax,
		},
		{
			name: "rule 4: MaxToolCalls=0 means unlimited",
			eval: DefaultEvaluator{MinToolCalls: 0, MaxToolCalls: 0},
			traj: Trajectory{
				TerminalState: "completed",
				Success:       boolPtr(true),
				ToolCalls: []ToolCall{
					{Failed: false}, {Failed: false}, {Failed: false}, {Failed: false}, {Failed: false},
				},
			},
			eligible: true,
			reason:   ReasonOK,
		},
		{
			name: "rule 5: all tools failed",
			eval: DefaultEvaluator{MinToolCalls: 2},
			traj: Trajectory{
				TerminalState: "completed",
				Success:       boolPtr(true),
				ToolCalls:     []ToolCall{{Failed: true}, {Failed: true}},
			},
			eligible: false,
			reason:   ReasonAllToolCallsFailed,
		},
		{
			name: "rule 5: at least one not-failed is OK",
			eval: DefaultEvaluator{MinToolCalls: 2},
			traj: Trajectory{
				TerminalState: "completed",
				Success:       boolPtr(true),
				ToolCalls:     []ToolCall{{Failed: true}, {Failed: false}},
			},
			eligible: true,
			reason:   ReasonOK,
		},
	}
}

func TestEvaluator_InterruptedTrajectoryIsIneligible(t *testing.T) {
	e := DefaultEvaluator{MinToolCalls: 2}
	v := e.Evaluate(Trajectory{TerminalState: "interrupted", ToolCalls: []ToolCall{{}, {}}})
	if v.Eligible || v.Reason != ReasonNonCompletedTerminal {
		t.Fatalf("got %+v", v)
	}
}

func TestEvaluator_AbortedTrajectoryIsIneligible(t *testing.T) {
	e := DefaultEvaluator{MinToolCalls: 2}
	v := e.Evaluate(Trajectory{TerminalState: "aborted", ToolCalls: []ToolCall{{}, {}}})
	if v.Eligible || v.Reason != ReasonNonCompletedTerminal {
		t.Fatalf("got %+v", v)
	}
}

func TestEvaluator_BelowMinToolCallsIsIneligible(t *testing.T) {
	e := DefaultEvaluator{MinToolCalls: 5}
	v := e.Evaluate(Trajectory{
		TerminalState: "completed",
		Success:       boolPtr(true),
		ToolCalls:     []ToolCall{{}, {}},
	})
	if v.Eligible || v.Reason != ReasonToolCallsBelowMin {
		t.Fatalf("got %+v", v)
	}
}

func TestEvaluator_AllToolCallsFailedIsIneligible(t *testing.T) {
	e := DefaultEvaluator{MinToolCalls: 1}
	v := e.Evaluate(Trajectory{
		TerminalState: "completed",
		Success:       boolPtr(true),
		ToolCalls:     []ToolCall{{Failed: true}},
	})
	if v.Eligible || v.Reason != ReasonAllToolCallsFailed {
		t.Fatalf("got %+v", v)
	}
}

func TestEvaluator_NilSuccessTreatedAsEligible(t *testing.T) {
	e := DefaultEvaluator{MinToolCalls: 0}
	v := e.Evaluate(Trajectory{TerminalState: "completed", Success: nil})
	if !v.Eligible || v.Reason != ReasonOK {
		t.Fatalf("got %+v", v)
	}
}

func TestEvaluator_DeterministicAcrossRuns(t *testing.T) {
	e := DefaultEvaluator{MinToolCalls: 2, MaxToolCalls: 5}
	traj := Trajectory{
		TerminalState: "completed",
		Success:       boolPtr(true),
		ToolCalls: []ToolCall{
			{CallID: "a", Failed: false},
			{CallID: "b", Failed: true},
			{CallID: "c", Failed: false},
		},
	}
	first := e.Evaluate(traj)
	for i := 0; i < 10; i++ {
		if got := e.Evaluate(traj); got != first {
			t.Fatalf("nondeterministic: run %d returned %+v vs first %+v", i, got, first)
		}
	}
}

func TestEvaluator_DoesNotMutateInput(t *testing.T) {
	// Defence: Evaluate must not reorder or mutate the input ToolCalls.
	e := DefaultEvaluator{MinToolCalls: 2}
	traj := Trajectory{
		TerminalState: "completed",
		Success:       boolPtr(true),
		ToolCalls: []ToolCall{
			{CallID: "a", Failed: false},
			{CallID: "b", Failed: false},
		},
	}
	before := make([]ToolCall, len(traj.ToolCalls))
	copy(before, traj.ToolCalls)
	_ = e.Evaluate(traj)
	for i := range before {
		if traj.ToolCalls[i] != before[i] {
			t.Fatalf("Evaluate mutated ToolCalls[%d]", i)
		}
	}
}
