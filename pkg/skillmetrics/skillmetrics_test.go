package skillmetrics

import "testing"

// TestCountersIncrementAndSnapshot 验证所有 counter 的 Inc/Read/Snapshot 对齐，
// 并保证 ResetForTesting 能重置到 0。测试使用 t.Cleanup 恢复全局状态，避免
// 污染其他测试（如 prompt skill_catalog_fx_test 也读这些 counter 时的干扰）。
func TestCountersIncrementAndSnapshot(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	IncSkillInvalidMode()
	IncSkillInvalidMode()
	IncUntrustedManifestRedaction()
	IncTrimCorruptionFallback()
	IncArtifactApprovalMiss()
	IncArtifactApprovalMiss()
	IncArtifactApprovalMiss()
	IncSkillExpandInvoke()
	IncSkillMCPToolCall()
	IncSkillMCPToolCall()
	IncSkillMCPToolSuccess()
	IncSkillMCPToolError()
	IncSkillMCPApprovalRequired()
	IncHostToolCallOutcome(HostToolOutcomeOK)
	IncHostToolCallOutcome(HostToolOutcomeOK)
	IncHostToolCallOutcome(HostToolOutcomeCWDMissing)
	IncHostToolCallOutcome(HostToolOutcomeApprovalRequired)
	IncHostToolCallOutcome(HostToolOutcomeError)
	IncHostToolCallOutcome("unknown")
	IncEnrichFailure()
	IncEnrichFailure()

	if v := SkillInvalidMode(); v != 2 {
		t.Fatalf("SkillInvalidMode want 2, got %d", v)
	}
	if v := UntrustedManifestRedaction(); v != 1 {
		t.Fatalf("UntrustedManifestRedaction want 1, got %d", v)
	}
	if v := TrimCorruptionFallback(); v != 1 {
		t.Fatalf("TrimCorruptionFallback want 1, got %d", v)
	}
	if v := ArtifactApprovalMiss(); v != 3 {
		t.Fatalf("ArtifactApprovalMiss want 3, got %d", v)
	}
	if v := SkillExpandInvoke(); v != 1 {
		t.Fatalf("SkillExpandInvoke want 1, got %d", v)
	}
	if v := SkillMCPToolCall(); v != 2 {
		t.Fatalf("SkillMCPToolCall want 2, got %d", v)
	}
	if v := SkillMCPToolSuccess(); v != 1 {
		t.Fatalf("SkillMCPToolSuccess want 1, got %d", v)
	}
	if v := SkillMCPToolError(); v != 1 {
		t.Fatalf("SkillMCPToolError want 1, got %d", v)
	}
	if v := SkillMCPApprovalRequired(); v != 1 {
		t.Fatalf("SkillMCPApprovalRequired want 1, got %d", v)
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

	snap := Read()
	if snap.SkillInvalidModeTotal != 2 ||
		snap.UntrustedManifestRedactionTotal != 1 ||
		snap.TrimCorruptionFallbackCount != 1 ||
		snap.ArtifactApprovalMissTotal != 3 ||
		snap.SkillExpandInvokeRate != 1 ||
		snap.SkillMCPToolCallTotal != 2 ||
		snap.SkillMCPToolSuccessTotal != 1 ||
		snap.SkillMCPToolErrorTotal != 1 ||
		snap.SkillMCPApprovalRequiredTotal != 1 ||
		snap.HostToolCallOKTotal != 2 ||
		snap.HostToolCallCWDMissingTotal != 1 ||
		snap.HostToolCallApprovalReqTotal != 1 ||
		snap.HostToolCallErrorTotal != 2 ||
		snap.EnrichFailuresTotal != 2 {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}
}

func TestResetForTestingZeroes(t *testing.T) {
	IncSkillInvalidMode()
	IncSkillExpandInvoke()
	IncSkillMCPToolCall()
	IncSkillMCPToolSuccess()
	IncSkillMCPToolError()
	IncSkillMCPApprovalRequired()
	IncHostToolCallOutcome(HostToolOutcomeOK)
	IncHostToolCallOutcome(HostToolOutcomeCWDMissing)
	IncHostToolCallOutcome(HostToolOutcomeApprovalRequired)
	IncHostToolCallOutcome(HostToolOutcomeError)
	IncEnrichFailure()
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	if got := Read(); got != (Snapshot{}) {
		t.Fatalf("after Reset want zero snapshot, got %+v", got)
	}
}
