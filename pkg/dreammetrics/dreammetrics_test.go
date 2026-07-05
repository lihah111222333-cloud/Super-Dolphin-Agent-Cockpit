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
	assertDreamSnapshotCounts(t, snap)
	assertDreamSingleReadsMatchSnapshot(t, snap)
}

func assertDreamSnapshotCounts(t *testing.T, snap Snapshot) {
	t.Helper()

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
}

func assertDreamSingleReadsMatchSnapshot(t *testing.T, snap Snapshot) {
	t.Helper()

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
	AddTokens(100, 50, 200)
	ResetForTesting()

	snap := Read()
	if snap != (Snapshot{}) {
		t.Errorf("expected zero snapshot after reset, got %+v", snap)
	}
}

// TestDreamMetrics_AddTokensAccumulates 验证 token counter 聚合 API 的累加语义与读取一致性。
// AddTokens 是 Step 2 接入 dream provider 解析 usage 后的入口，本测锁 Step 1 API 语义。
func TestDreamMetrics_AddTokensAccumulates(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	AddTokens(100, 50, 200)
	AddTokens(30, 10, 0)

	if got := TokensInput(); got != 130 {
		t.Errorf("TokensInput: got %d, want 130", got)
	}
	if got := TokensOutput(); got != 60 {
		t.Errorf("TokensOutput: got %d, want 60", got)
	}
	if got := TokensCacheRead(); got != 200 {
		t.Errorf("TokensCacheRead: got %d, want 200", got)
	}

	snap := Read()
	if snap.TokensInputTotal != TokensInput() ||
		snap.TokensOutputTotal != TokensOutput() ||
		snap.TokensCacheReadTotal != TokensCacheRead() {
		t.Errorf("snapshot/single-read mismatch: %+v", snap)
	}
}

// TestDreamMetrics_AddTokensZeroIsNoop 验证【 0 值参数隐式 no-op】语义。
// dream provider 某次只拿到部分 usage 字段时，可以安全传 0，不会污染 counter。
func TestDreamMetrics_AddTokensZeroIsNoop(t *testing.T) {
	ResetForTesting()
	t.Cleanup(ResetForTesting)

	AddTokens(0, 0, 0)

	if TokensInput() != 0 || TokensOutput() != 0 || TokensCacheRead() != 0 {
		t.Errorf("AddTokens(0,0,0) should be no-op, got snapshot %+v", Read())
	}
}
