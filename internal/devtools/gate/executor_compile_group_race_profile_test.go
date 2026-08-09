package gate

import (
	"testing"
	"time"
)

func TestCompiledSelectorBatchBindsRaceGoFlags(t *testing.T) {
	first := mustRaceCompileGroupBatchWorkload(t, "TestCompileGroup")
	second := mustRaceCompileGroupBatchWorkload(t, "TestCompileGroupSecond")
	group := CompileGroup{PackageTarget: "./sample", SemanticKey: CompileGroupSemanticGoTestRace, WorkloadIDs: []GateID{GateID(first.ID), GateID(second.ID)}}
	_, specs := mustCompileGroupBatchCommand(t, group)
	base := time.UnixMilli(7_500_000).UTC()
	observation := compiledSelectorBatchObservation{
		started: base, bodyStarted: base.Add(time.Millisecond), completed: base.Add(10 * time.Millisecond),
		selectorTimings: map[string][]GoTestTiming{
			specs[GateID(first.ID)].name:  {{Name: specs[GateID(first.ID)].name, Status: GoTestStatusPass, DurationMS: 2}},
			specs[GateID(second.ID)].name: {{Name: specs[GateID(second.ID)].name, Status: GoTestStatusPass, DurationMS: 3}},
		},
		selectorIntervals: map[string]compiledSelectorBatchInterval{
			specs[GateID(first.ID)].name:  {runAt: base.Add(time.Millisecond), completedAt: base.Add(3 * time.Millisecond)},
			specs[GateID(second.ID)].name: {runAt: base.Add(time.Millisecond), completedAt: base.Add(4 * time.Millisecond)},
		},
	}
	results, err := compiledSelectorBatchResults(group, []string{"go", "tool", "test2json"}, specs, observation)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range group.WorkloadIDs {
		if got := results[id].ExecutionProfile.GoFlags; got != CanonicalGoFlags(true) {
			t.Fatalf("race selector %q GoFlags = %q, want %q", id, got, CanonicalGoFlags(true))
		}
	}
}

func mustRaceCompileGroupBatchWorkload(t *testing.T, name string) Workload {
	t.Helper()
	workload, err := NewGoTestWorkload(GateIDBackendTestGuardWithRace, "./sample", name, 10)
	if err != nil {
		t.Fatal(err)
	}
	return workload
}
