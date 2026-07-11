//go:build manual

// Package claudecli manual test：用真实 claude binary + ~/.claude 凭据
// 端到端验证 dream_executor wrapper 链路。
//
// 跑法：
//
//	go test -tags=manual -run TestManualClaudeDreamPipeline -v ./internal/provider/claudecli/
//
// 不带 -tags=manual 不会编译，CI 自动跳过。
package claudecli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/dreammetrics"
)

// minimalConsolidationPrompt 模拟 consolidation_prompt.go 的 JSON 契约要求。
// 不传任何真实 memory 数据，仅验证 LLM 能按要求输出空 memories envelope。
const minimalConsolidationPrompt = `You are a memory consolidation assistant. Your job is to output a strict JSON object with this exact schema:

{"memories":[]}

Rules:
1. Output ONLY the JSON object, no prose, no preamble, no markdown fence.
2. Since the input has no memory items to consolidate, return empty memories list.
3. The JSON must be valid and parseable.

Now output the JSON:`

func TestManualClaudeDreamPipeline(t *testing.T) {
	// 5min timeout 与 dispatcher 默认对齐
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)

	// 使用生产构造器（commander=nil → NewRealCommander，binary 走 resolveBinaryPath）
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

	// 验证 1: 输出非空
	if strings.TrimSpace(got) == "" {
		t.Fatal("expected non-empty output, got empty string")
	}

	// 验证 2: 输出是有效 JSON 且包含 memories key
	var envelope struct {
		Memories []map[string]any `json:"memories"`
	}
	if err := json.Unmarshal([]byte(got), &envelope); err != nil {
		t.Fatalf("expected valid JSON envelope, got parse error: %v\nraw: %s", err, got)
	}
	t.Logf("parsed envelope: memories=%d items", len(envelope.Memories))
	if got := dreammetrics.TokensInput(); got == 0 {
		t.Errorf("TokensInput() = %d, want > 0 (claude usage should be recorded)", got)
	}

	// 验证 3: parseExtractedMemories 兼容（使用 memory 模块的真实 parser 验证 JSON 契约）
	// 注：parseExtractedMemories 在 internal/module/memory，这里只验证 JSON 结构兼容性
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "...[truncated]"
}
