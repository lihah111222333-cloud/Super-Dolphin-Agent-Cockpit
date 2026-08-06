package skillmetrics

import "testing"

// TestCountersIncrementAndSnapshot 验证独立 Registry 的七条 series 对齐。
func TestCountersIncrementAndSnapshot(t *testing.T) {
	registry := NewRegistry()

	registry.IncTrimCorruptionFallback()
	registry.IncHostToolCallOutcome(HostToolOutcomeOK)
	registry.IncHostToolCallOutcome(HostToolOutcomeOK)
	registry.IncHostToolCallOutcome(HostToolOutcomeCWDMissing)
	registry.IncHostToolCallOutcome(HostToolOutcomeApprovalRequired)
	registry.IncHostToolCallOutcome(HostToolOutcomeError)
	registry.IncHostToolCallOutcome("unknown") // 未知 outcome 计入 error
	registry.IncEnrichFailure()
	registry.IncEnrichFailure()

	assertSkillSnapshot(t, registry.Snapshot())
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

func TestNilRegistryFailsFast(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("nil registry must panic")
		}
	}()
	var registry *Registry
	registry.IncEnrichFailure()
}
