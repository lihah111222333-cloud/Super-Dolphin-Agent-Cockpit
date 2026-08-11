package gate

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunCompileGroupGateFailsMissingBatchWithoutArtifactFallback(t *testing.T) {
	workload := mustCompileGroupBatchWorkload(t, "TestStatelessGuard")
	id := GateID(workload.ID)
	result, err := runCompileGroupGate(
		context.Background(), 0, id, nil,
		map[GateID]compiledGroupArtifact{id: {}}, nil, nil, "", "",
	)
	if err == nil || !strings.Contains(err.Error(), "no batched result") {
		t.Fatalf("missing batch error = %v, want fail-fast result-coverage error", err)
	}
	if result.Status != ResultStatusFailed || !strings.Contains(string(result.Log), "no batched result") {
		t.Fatalf("missing batch result = %#v, want diagnostic failed result", result)
	}
}

func TestRunCompileGroupGateKeepsSiblingStructuredAfterCompileFailure(t *testing.T) {
	first := mustCompileGroupBatchWorkload(t, "TestFirstCompileFailure")
	second := mustCompileGroupBatchWorkload(t, "TestSecondCompileAfterFailure")
	firstID, secondID := GateID(first.ID), GateID(second.ID)
	executions := []CompileGroupExecution{
		{GroupID: digestPlanLog([]byte("failed-group")), WorkloadIDs: []GateID{firstID}, Status: ResultStatusFailed, ExitCode: 1, ErrorText: "compile failed"},
		{GroupID: digestPlanLog([]byte("sibling-group")), WorkloadIDs: []GateID{secondID}, Status: ResultStatusFailed, ExitCode: 1, ErrorText: "compile failed"},
	}
	for _, id := range []GateID{firstID, secondID} {
		result, err := runCompileGroupGate(context.Background(), 0, id, nil, nil, executions, nil, "", "")
		if err != nil {
			t.Fatalf("compile failure for %q cancelled sibling structure: %v", id, err)
		}
		if result.Status != ResultStatusFailed || !strings.Contains(string(result.Log), "compile failed") {
			t.Fatalf("compile failure result for %q = %#v, want structured failure", id, result)
		}
		if !result.StartedAt.Equal(result.CompletedAt) || result.ExecutionProfile.StartupMS != 0 || result.ExecutionProfile.TestBodyMS != 0 || result.ExecutionProfile.TotalMS != 0 {
			t.Fatalf("compile-before-start failure for %q fabricated measured timing: %#v", id, result)
		}
	}
}

func TestRunCompileGroupGateRejectsInvalidBatchIntervalWithoutArtifactFallback(t *testing.T) {
	workload := mustCompileGroupBatchWorkload(t, "TestStatelessGuard")
	id := GateID(workload.ID)
	base := time.UnixMilli(8_000_000)
	batched := PlanGateExecution{
		GateID: id, Status: ResultStatusPassed, ExitCode: 0,
		StartedAt: base, CompletedAt: base,
		TestTimings:      []GoTestTiming{{Name: "TestStatelessGuard", Status: GoTestStatusPass, DurationMS: 200}},
		ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 200, TotalMS: 201},
	}
	result, err := runCompileGroupGate(
		context.Background(), 0, id, map[GateID]PlanGateExecution{id: batched},
		map[GateID]compiledGroupArtifact{id: {}}, nil, nil, "", "",
	)
	if err == nil || !strings.Contains(err.Error(), "interval is invalid") {
		t.Fatalf("invalid interval error = %v, want fail-fast interval error", err)
	}
	if result.Status != ResultStatusFailed || !strings.Contains(string(result.Log), "interval is invalid") {
		t.Fatalf("invalid interval result = %#v, want diagnostic failed result", result)
	}
}

func TestRunCompileGroupGateUsesCanonicalBatchProfile(t *testing.T) {
	workload := mustCompileGroupBatchWorkload(t, "TestStatelessGuard")
	id := GateID(workload.ID)
	started := time.UnixMilli(8_000_000).Add(244 * time.Second)
	completed := started.Add(201 * time.Millisecond)
	want := PlanGateExecution{
		GateID: id, Status: ResultStatusPassed, ExitCode: 0,
		StartedAt: started, CompletedAt: completed,
		TestTimings:      []GoTestTiming{{Name: "TestStatelessGuard", Status: GoTestStatusPass, DurationMS: 200}},
		ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 200, TotalMS: 201},
	}
	got, err := runCompileGroupGate(
		context.Background(), 0, id, map[GateID]PlanGateExecution{id: want},
		map[GateID]compiledGroupArtifact{id: {}}, nil, nil, "", "",
	)
	if err != nil {
		t.Fatal(err)
	}
	if got.ExecutionProfile != want.ExecutionProfile || !got.StartedAt.Equal(started) || !got.CompletedAt.Equal(completed) {
		t.Fatalf("batch result = %#v, want canonical 200ms selector projection", got)
	}
}
