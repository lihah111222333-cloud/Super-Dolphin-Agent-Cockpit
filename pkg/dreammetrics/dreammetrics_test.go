package dreammetrics

import "testing"

func TestDreamMetrics_IncrementAndRead(t *testing.T) {
	registry := NewRegistry()

	registry.IncSuccess()
	registry.IncSuccess()
	registry.IncProviderSkipped()
	registry.IncProviderFailed()
	registry.IncProviderFailed()
	registry.IncProviderFailed()
	registry.IncAllNotConfigured()
	registry.IncPromptOversize()
	registry.IncPromptOversize()

	snap := registry.Read()
	assertDreamSnapshotCounts(t, snap)
	assertDreamSingleReadsMatchSnapshot(t, registry, snap)
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

func assertDreamSingleReadsMatchSnapshot(t *testing.T, registry *Registry, snap Snapshot) {
	t.Helper()

	// 单值读 API 与 snapshot 一致
	if registry.Success() != snap.SuccessTotal {
		t.Errorf("Success()/SuccessTotal mismatch: %d vs %d", registry.Success(), snap.SuccessTotal)
	}
	if registry.ProviderSkipped() != snap.ProviderSkippedTotal {
		t.Errorf("ProviderSkipped() mismatch")
	}
	if registry.ProviderFailed() != snap.ProviderFailedTotal {
		t.Errorf("ProviderFailed() mismatch")
	}
	if registry.AllNotConfigured() != snap.AllNotConfiguredTotal {
		t.Errorf("AllNotConfigured() mismatch")
	}
	if registry.PromptOversize() != snap.PromptOversizeTotal {
		t.Errorf("PromptOversize() mismatch")
	}
}

func TestDreamMetrics_ResetForTestingZeroes(t *testing.T) {
	registry := NewRegistry()
	registry.IncSuccess()
	registry.IncProviderSkipped()
	registry.IncProviderFailed()
	registry.IncAllNotConfigured()
	registry.IncPromptOversize()
	registry.AddTokens(100, 50, 200)
	registry.ResetForTesting()

	snap := registry.Read()
	if snap != (Snapshot{}) {
		t.Errorf("expected zero snapshot after reset, got %+v", snap)
	}
}

// TestDreamMetrics_AddTokensAccumulates 验证 token counter 聚合 API 的累加语义与读取一致性。
// AddTokens 是 Step 2 接入 dream provider 解析 usage 后的入口，本测锁 Step 1 API 语义。
func TestDreamMetrics_AddTokensAccumulates(t *testing.T) {
	registry := NewRegistry()

	registry.AddTokens(100, 50, 200)
	registry.AddTokens(30, 10, 0)

	if got := registry.TokensInput(); got != 130 {
		t.Errorf("TokensInput: got %d, want 130", got)
	}
	if got := registry.TokensOutput(); got != 60 {
		t.Errorf("TokensOutput: got %d, want 60", got)
	}
	if got := registry.TokensCacheRead(); got != 200 {
		t.Errorf("TokensCacheRead: got %d, want 200", got)
	}

	snap := registry.Read()
	if snap.TokensInputTotal != registry.TokensInput() ||
		snap.TokensOutputTotal != registry.TokensOutput() ||
		snap.TokensCacheReadTotal != registry.TokensCacheRead() {
		t.Errorf("snapshot/single-read mismatch: %+v", snap)
	}
}

// TestDreamMetrics_AddTokensZeroIsNoop 验证【 0 值参数隐式 no-op】语义。
// dream provider 某次只拿到部分 usage 字段时，可以安全传 0，不会污染 counter。
func TestDreamMetrics_AddTokensZeroIsNoop(t *testing.T) {
	registry := NewRegistry()

	registry.AddTokens(0, 0, 0)

	if registry.TokensInput() != 0 || registry.TokensOutput() != 0 || registry.TokensCacheRead() != 0 {
		t.Errorf("AddTokens(0,0,0) should be no-op, got snapshot %+v", registry.Read())
	}
}
