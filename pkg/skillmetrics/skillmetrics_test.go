package skillmetrics

import "testing"

// TestCountersIncrementAndSnapshot 验证活 counter 的 Inc/Read/Snapshot 对齐，
// 并保证 ResetForTesting 能重置到 0。
func TestCountersIncrementAndSnapshot(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	IncTrimCorruptionFallback()
	IncHostToolCallOutcome(HostToolOutcomeOK)
	IncHostToolCallOutcome(HostToolOutcomeOK)
	IncHostToolCallOutcome(HostToolOutcomeCWDMissing)
	IncHostToolCallOutcome(HostToolOutcomeApprovalRequired)
	IncHostToolCallOutcome(HostToolOutcomeError)
	IncHostToolCallOutcome("unknown") // 未知 outcome 计入 error
	IncEnrichFailure()
	IncEnrichFailure()

	assertCounterReads(t)
	assertSkillSnapshot(t, Read())
}

func assertCounterReads(t *testing.T) {
	t.Helper()

	if v := TrimCorruptionFallback(); v != 1 {
		t.Fatalf("TrimCorruptionFallback want 1, got %d", v)
	}
	if v := HostToolCallOK(); v != 2 {
		t.Fatalf("HostToolCallOK want 2, got %d", v)
	}
	if v := HostToolCallCWDMissing(); v != 1 {
		t.Fatalf("HostToolCallCWDMissing want 1, got %d", v)
	}
	if v := HostToolCallApprovalRequired(); v != 1 {
		t.Fatalf("HostToolCallApprovalRequired want 1, got %d", v)
	}
	if v := HostToolCallError(); v != 2 {
		t.Fatalf("HostToolCallError want 2, got %d", v)
	}
	if v := EnrichFailures(); v != 2 {
		t.Fatalf("EnrichFailures want 2, got %d", v)
	}
}

func assertSkillSnapshot(t *testing.T, snap Snapshot) {
	t.Helper()

	if snap.TrimCorruptionFallbackCount != 1 ||
		snap.HostToolCallOKTotal != 2 ||
		snap.HostToolCallCWDMissingTotal != 1 ||
		snap.HostToolCallApprovalReqTotal != 1 ||
		snap.HostToolCallErrorTotal != 2 ||
		snap.EnrichFailuresTotal != 2 {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}
}

func TestResetForTestingZeroes(t *testing.T) {
	IncTrimCorruptionFallback()
	IncHostToolCallOutcome(HostToolOutcomeOK)
	IncHostToolCallOutcome(HostToolOutcomeError)
	IncEnrichFailure()
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	if got := Read(); got != (Snapshot{}) {
		t.Fatalf("after Reset want zero snapshot, got %+v", got)
	}
}
