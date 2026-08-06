package agent

import "testing"

func TestLifecycleDefinitionSnapshotsAreIndependent(t *testing.T) {
	t.Parallel()

	statesFirst, statesSecond := StateDefinitions(), StateDefinitions()
	statesFirst[0].Description = "mutated"
	if statesSecond[0].Description == "mutated" {
		t.Fatal("StateDefinitions returned shared mutable state")
	}

	triggersFirst, triggersSecond := TriggerDefinitions(), TriggerDefinitions()
	triggersFirst[0].Description = "mutated"
	if triggersSecond[0].Description == "mutated" {
		t.Fatal("TriggerDefinitions returned shared mutable state")
	}

	transitionsFirst, transitionsSecond := TransitionDefinitions(), TransitionDefinitions()
	transitionsFirst[0].To = StateFailed
	if transitionsSecond[0].To == StateFailed {
		t.Fatal("TransitionDefinitions returned shared mutable state")
	}
}
