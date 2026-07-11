//go:build manual

// Package codexapp manual test：用真实 codex binary + ~/.codex 凭据
// 端到端验证 dream_executor wrapper 链路。
//
// 跑法：
//
//	go test -tags=manual -run TestManualCodexDreamPipeline -v ./internal/provider/codexapp/
//
// 不带 -tags=manual 不会编译，CI 自动跳过。
package codexapp

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dreammetrics"
)

// minimalConsolidationPrompt 与 claudecli 端到端手测对称，
// 验证 codex 也能严格按 JSON 契约输出。
const minimalConsolidationPrompt = `You are a memory consolidation assistant. Your job is to output a strict JSON object with this exact schema:

{"memories":[]}

Rules:
1. Output ONLY the JSON object, no prose, no preamble, no markdown fence.
2. Since the input has no memory items to consolidate, return empty memories list.
3. The JSON must be valid and parseable.

Now output the JSON:`

func TestManualCodexDreamPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)

	exec := newDreamExecutor(nil, "", "")
	t.Logf("dream executor: binary=%q, model=%q", exec.binary, exec.model)

	start := time.Now()
	got, err := exec.ExecuteDream(ctx, minimalConsolidationPrompt)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("ExecuteDream failed after %v: %v", elapsed, err)
	}
	t.Logf("ExecuteDream returned %d bytes in %v", len(got), elapsed)
	t.Logf("raw output (first 500 chars): %s", truncate(got, 500))

	if strings.TrimSpace(got) == "" {
		t.Fatal("expected non-empty output, got empty string")
	}

	var envelope struct {
		Memories []map[string]any `json:"memories"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("expected valid JSON envelope, got parse error: %v\nraw: %s", err, got)
	}
	t.Logf("parsed envelope: memories=%d items", len(envelope.Memories))
	if got := dreammetrics.TokensInput(); got == 0 {
		t.Errorf("TokensInput() = %d, want > 0 (codex usage should be recorded)", got)
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
