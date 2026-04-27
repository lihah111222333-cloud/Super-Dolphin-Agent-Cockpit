package dreammetrics

import "testing"

func TestDreamMetrics_IncrementAndRead(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	IncSuccess()
	IncSuccess()
	IncProviderSkipped()
	IncProviderFailed()
	IncProviderFailed()
	IncProviderFailed()
	IncAllNotConfigured()
	IncPromptOversize()
	IncPromptOversize()

	snap := Read()
	if snap.SuccessTotal != 2 {
		t.Errorf("SuccessTotal: got %d, want 2", snap.SuccessTotal)
	}
	if snap.ProviderSkippedTotal != 1 {
		t.Errorf("ProviderSkippedTotal: got %d, want 1", snap.ProviderSkippedTotal)
	}
	if snap.ProviderFailedTotal != 3 {
		t.Errorf("ProviderFailedTotal: got %d, want 3", snap.ProviderFailedTotal)
	}
	if snap.AllNotConfiguredTotal != 1 {
		t.Errorf("AllNotConfiguredTotal: got %d, want 1", snap.AllNotConfiguredTotal)
	}
	if snap.PromptOversizeTotal != 2 {
		t.Errorf("PromptOversizeTotal: got %d, want 2", snap.PromptOversizeTotal)
	}

	// 单值读 API 与 snapshot 一致
	if Success() != snap.SuccessTotal {
		t.Errorf("Success()/SuccessTotal mismatch: %d vs %d", Success(), snap.SuccessTotal)
	}
	if ProviderSkipped() != snap.ProviderSkippedTotal {
		t.Errorf("ProviderSkipped() mismatch")
	}
	if ProviderFailed() != snap.ProviderFailedTotal {
		t.Errorf("ProviderFailed() mismatch")
	}
	if AllNotConfigured() != snap.AllNotConfiguredTotal {
		t.Errorf("AllNotConfigured() mismatch")
	}
	if PromptOversize() != snap.PromptOversizeTotal {
		t.Errorf("PromptOversize() mismatch")
	}
}

func TestDreamMetrics_ResetForTestingZeroes(t *testing.T) {
	IncSuccess()
	IncProviderSkipped()
	IncProviderFailed()
	IncAllNotConfigured()
	IncPromptOversize()
	ResetForTesting()

	snap := Read()
	if snap != (Snapshot{}) {
		t.Errorf("expected zero snapshot after reset, got %+v", snap)
	}
}
