package skillmetrics

import "testing"

// TestCountersIncrementAndSnapshot 验证 5 个 counter 的 Inc/Read/Snapshot 对齐，
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

	snap := Read()
	if snap.SkillInvalidModeTotal != 2 ||
		snap.UntrustedManifestRedactionTotal != 1 ||
		snap.TrimCorruptionFallbackCount != 1 ||
		snap.ArtifactApprovalMissTotal != 3 ||
		snap.SkillExpandInvokeRate != 1 {
		t.Fatalf("snapshot mismatch: %+v", snap)
	}
}

func TestResetForTestingZeroes(t *testing.T) {
	IncSkillInvalidMode()
	IncSkillExpandInvoke()
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	if got := Read(); got != (Snapshot{}) {
		t.Fatalf("after Reset want zero snapshot, got %+v", got)
	}
}
