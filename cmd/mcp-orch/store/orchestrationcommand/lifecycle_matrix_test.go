package orchestrationcommand

import (
	"errors"
	"testing"
)

type lifecycleMatrixSpec struct {
	phases   []Phase
	triggers []Trigger
	wants    map[Phase]map[Trigger]Phase
}

func testLifecycleStateMatrix(t *testing.T) {
	actual := phaseTransitions()
	expected := frozenLifecycleTransitions()
	assertLifecycleTransitionSet(t, actual, expected)
	assertLifecycleTypeCollections(t, actual, expected)
	spec := newLifecycleMatrixSpec(t, expected)
	assertLifecycleMatrixCells(t, spec)
	assertUnknownHasNoRetryOrDispatch(t)
	assertTerminalPhasesHaveNoOutgoingEdges(t, spec.triggers)
}

func frozenLifecycleTransitions() []phaseTransition {
	return []phaseTransition{
		{PhaseCustodyPending, TriggerAccept, PhaseAccepted},
		{PhaseCustodyPending, TriggerAbortAccept, PhaseRejected},
		{PhaseAccepted, TriggerBeginDispatch, PhaseDispatchPending},
		{PhaseAccepted, TriggerRejectBeforeDispatch, PhaseRejected},
		{PhaseDispatchPending, TriggerReclaimUnsent, PhaseDispatchPending},
		{PhaseDispatchPending, TriggerConfirmDispatch, PhaseDispatched},
		{PhaseDispatchPending, TriggerAbortDispatch, PhaseDispatchAborted},
		{PhaseDispatchAborted, TriggerRejectBeforeDispatch, PhaseRejected},
		{PhaseDispatched, TriggerRedeemEffect, PhaseEffectPending},
		{PhaseEffectPending, TriggerIssueToken, PhaseTokenIssued},
		{PhaseEffectPending, TriggerAbortBeforeIssue, PhaseAbortedBeforeIssue},
		{PhaseTokenIssued, TriggerStartEffect, PhaseEffectStarted},
		{PhaseTokenIssued, TriggerAbortBeforeStart, PhaseAbortedBeforeStart},
		{PhaseAbortedBeforeIssue, TriggerBeginCleanup, PhaseCleanupPending},
		{PhaseAbortedBeforeStart, TriggerBeginCleanup, PhaseCleanupPending},
		{PhaseCleanupPending, TriggerFinishCleanup, PhaseRejected},
		{PhaseEffectStarted, TriggerProviderAck, PhaseProviderAcknowledged},
		{PhaseProviderAcknowledged, TriggerComplete, PhaseCompleted},
		{PhaseEffectStarted, TriggerFailEffect, PhaseFailed},
		{PhaseProviderAcknowledged, TriggerFailEffect, PhaseFailed},
		{PhaseTokenIssued, TriggerMarkUnknown, PhaseUnknown},
		{PhaseEffectStarted, TriggerMarkUnknown, PhaseUnknown},
		{PhaseProviderAcknowledged, TriggerMarkUnknown, PhaseUnknown},
		{PhaseUnknown, TriggerReconcileAck, PhaseProviderAcknowledged},
		{PhaseUnknown, TriggerReconcileComplete, PhaseCompleted},
		{PhaseUnknown, TriggerReconcileFailed, PhaseFailed},
		{PhaseUnknown, TriggerReconcileUnstarted, PhaseAbortedBeforeStart},
	}
}

func assertLifecycleTransitionSet(t *testing.T, actual, expected []phaseTransition) {
	t.Helper()
	if len(expected) != 27 {
		t.Fatalf("independent lifecycle transition contract has %d edges, want 27", len(expected))
	}
	actualSet := lifecycleTransitionSet(t, "actual", actual)
	expectedSet := lifecycleTransitionSet(t, "independent", expected)
	var missing, extra []phaseTransition
	for edge := range expectedSet {
		if !actualSet[edge] {
			missing = append(missing, edge)
		}
	}
	for edge := range actualSet {
		if !expectedSet[edge] {
			extra = append(extra, edge)
		}
	}
	if len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("lifecycle transition edge drift: missing=%v extra=%v", missing, extra)
	}
}

func lifecycleTransitionSet(t *testing.T, name string, rules []phaseTransition) map[phaseTransition]bool {
	t.Helper()
	result := make(map[phaseTransition]bool, len(rules))
	for _, rule := range rules {
		if result[rule] {
			t.Fatalf("%s lifecycle transition contract contains duplicate edge %#v", name, rule)
		}
		result[rule] = true
	}
	return result
}

func assertLifecycleTypeCollections(t *testing.T, actual, expected []phaseTransition) {
	t.Helper()
	actualPhases, actualTriggers := lifecycleTypeCollections(actual)
	expectedPhases, expectedTriggers := lifecycleTypeCollections(expected)
	assertLifecycleSetEqual(t, "phase", actualPhases, expectedPhases)
	assertLifecycleSetEqual(t, "trigger", actualTriggers, expectedTriggers)
}

func lifecycleTypeCollections(rules []phaseTransition) (map[Phase]bool, map[Trigger]bool) {
	phases := make(map[Phase]bool)
	triggers := make(map[Trigger]bool)
	for _, rule := range rules {
		phases[rule.From] = true
		phases[rule.To] = true
		triggers[rule.Trigger] = true
	}
	return phases, triggers
}

func assertLifecycleSetEqual[T comparable](t *testing.T, kind string, actual, expected map[T]bool) {
	t.Helper()
	var missing, extra []T
	for value := range expected {
		if !actual[value] {
			missing = append(missing, value)
		}
	}
	for value := range actual {
		if !expected[value] {
			extra = append(extra, value)
		}
	}
	if len(missing) != 0 || len(extra) != 0 {
		t.Fatalf("lifecycle %s type drift: missing=%v extra=%v", kind, missing, extra)
	}
}

func newLifecycleMatrixSpec(t *testing.T, rules []phaseTransition) lifecycleMatrixSpec {
	t.Helper()
	spec := lifecycleMatrixSpec{wants: make(map[Phase]map[Trigger]Phase)}
	if len(rules) == 0 {
		t.Fatal("phase transition table is empty")
	}
	for _, rule := range rules {
		appendLifecycleMatrixRule(t, &spec, rule)
	}
	spec.phases = append(spec.phases, Phase("unknown-phase"))
	spec.triggers = append(spec.triggers, Trigger("unknown-trigger"))
	return spec
}

func appendLifecycleMatrixRule(t *testing.T, spec *lifecycleMatrixSpec, rule phaseTransition) {
	t.Helper()
	if !containsLifecyclePhase(spec.phases, rule.From) {
		spec.phases = append(spec.phases, rule.From)
	}
	if !containsLifecyclePhase(spec.phases, rule.To) {
		spec.phases = append(spec.phases, rule.To)
	}
	if !containsLifecycleTrigger(spec.triggers, rule.Trigger) {
		spec.triggers = append(spec.triggers, rule.Trigger)
	}
	if spec.wants[rule.From] == nil {
		spec.wants[rule.From] = make(map[Trigger]Phase)
	}
	if prior, exists := spec.wants[rule.From][rule.Trigger]; exists && prior != rule.To {
		t.Fatalf("phase transition table has conflicting %s/%s rules", rule.From, rule.Trigger)
	}
	spec.wants[rule.From][rule.Trigger] = rule.To
}

func containsLifecyclePhase(phases []Phase, candidate Phase) bool {
	for _, phase := range phases {
		if phase == candidate {
			return true
		}
	}
	return false
}

func containsLifecycleTrigger(triggers []Trigger, candidate Trigger) bool {
	for _, trigger := range triggers {
		if trigger == candidate {
			return true
		}
	}
	return false
}

func assertLifecycleMatrixCells(t *testing.T, spec lifecycleMatrixSpec) {
	t.Helper()
	for _, phase := range spec.phases {
		for _, trigger := range spec.triggers {
			assertLifecycleMatrixCell(t, spec, phase, trigger)
		}
	}
}

func assertLifecycleMatrixCell(t *testing.T, spec lifecycleMatrixSpec, phase Phase, trigger Trigger) {
	t.Helper()
	got, err := nextCommandPhase(t.Context(), phase, trigger)
	want, allowed := spec.wants[phase][trigger]
	if allowed {
		if err != nil || got != want {
			t.Fatalf("nextCommandPhase(%s,%s) = %s/%v, want %s/nil", phase, trigger, got, err, want)
		}
		return
	}
	if !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("nextCommandPhase(%s,%s) error = %v, want ErrInvalidTransition", phase, trigger, err)
	}
}

func assertUnknownHasNoRetryOrDispatch(t *testing.T) {
	t.Helper()
	for _, trigger := range []Trigger{
		Trigger("retry"), TriggerBeginDispatch, TriggerReclaimUnsent,
		TriggerIssueToken, TriggerStartEffect,
	} {
		if _, err := nextCommandPhase(t.Context(), PhaseUnknown, trigger); !errors.Is(err, ErrInvalidTransition) {
			t.Fatalf("unknown phase accepted forbidden trigger %s: %v", trigger, err)
		}
	}
}

func assertTerminalPhasesHaveNoOutgoingEdges(t *testing.T, triggers []Trigger) {
	t.Helper()
	for _, phase := range []Phase{PhaseCompleted, PhaseFailed, PhaseRejected} {
		for _, trigger := range triggers {
			if _, err := nextCommandPhase(t.Context(), phase, trigger); !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf("terminal phase %s accepted trigger %s: %v", phase, trigger, err)
			}
		}
	}
}
