//go:build manual

// Package codexapp 端到端 manual test：验证 dispatcher 真 failover。
//
// 跑法：
//
//	go test -tags=manual -run TestManualDispatcherFailover -v ./internal/provider/codexapp/
//
// 测试场景：
//   - claudecli 故意指向不存在的 binary → 触发 ErrDreamExecutorNotConfigured
//   - codexapp 走真实 codex binary
//   - unified.NewDreamExecutor 字母序 first-wins (claude 先于 codex)
//   - 期望：dispatcher 试 claude → NotConfigured → 跳过 → 试 codex → 成功
package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/pkg/dreammetrics"
)

const failoverPrompt = `You are a memory consolidation assistant. Output ONLY this exact JSON: {"memories":[]}`

func TestManualDispatcherFailover(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// 故意把 claude binary 指向不存在路径，触发 ErrDreamExecutorNotConfigured
	t.Setenv("CLAUDE_CLI_BIN", "/nonexistent/claude-binary-for-failover-test")
	// codex 走默认 ~/.codex 凭据
	t.Setenv("DREAM_CODEX_BIN", "")

	// 重置 metrics counter 以隔离观察
	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)

	// 用 fx 拉起 dispatcher 太重，手动构造 provider 列表
	// 模仿 unified/module.go 内 dreamExecutorParams 收集流程
	providers := buildLiveProviders(t)
	dispatcher := unified.NewDreamExecutor(providers, nil)

	start := time.Now()
	got, err := dispatcher.ExecuteDream(ctx, failoverPrompt)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("dispatcher.ExecuteDream failed after %v: %v", elapsed, err)
	}

	t.Logf("dispatcher returned %d bytes in %v", len(got), elapsed)
	t.Logf("output (first 500 chars): %s", truncateForLog(got, 500))

	// 验证 1: 输出是有效 JSON
	var envelope struct {
		Memories []map[string]any `json:"memories"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("expected valid JSON envelope, got parse error: %v\nraw: %s", err, got)
	}
	t.Logf("parsed envelope: memories=%d items", len(envelope.Memories))

	// 验证 2: metrics 反映 failover 真发生
	snap := dreammetrics.Read()
	t.Logf("metrics snapshot: %+v", snap)

	if snap.ProviderSkippedTotal != 1 {
		t.Errorf("ProviderSkippedTotal: got %d, want 1 (claude should be skipped)", snap.ProviderSkippedTotal)
	}
	if snap.SuccessTotal != 1 {
		t.Errorf("SuccessTotal: got %d, want 1 (codex should succeed)", snap.SuccessTotal)
	}
	if snap.ProviderFailedTotal != 0 {
		t.Errorf("ProviderFailedTotal: got %d, want 0 (no real errors)", snap.ProviderFailedTotal)
	}
	if snap.AllNotConfiguredTotal != 0 {
		t.Errorf("AllNotConfiguredTotal: got %d, want 0 (codex succeeded)", snap.AllNotConfiguredTotal)
	}
	if got := dreammetrics.TokensInput(); got == 0 {
		t.Errorf("TokensInput() = %d, want > 0 (codex usage should be recorded)", got)
	}
}

// buildLiveProviders 构造与生产 fx 等价的 provider 列表。
func buildLiveProviders(t *testing.T) []contract.DreamExecutorProvider {
	t.Helper()
	return []contract.DreamExecutorProvider{
		claudecli.NewDreamExecutorProviderForManualTest(),
		provideDreamExecutorProvider(),
	}
}

func truncateForLog(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
