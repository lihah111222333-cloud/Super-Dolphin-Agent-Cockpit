//go:build manual

// 端到端 manual test：模拟 thread.stopped 事件触发 auto_dream 完整 pipeline。
// 跳过 thread.stopped → scheduler.Enqueue 那段（已被 scheduler 单测覆盖），
// 走 hooks.maybeScheduleAutoDream → SafeGo → consolidator.consolidateWithOptions
// → 真 dispatcher.ExecuteDream → 真 LLM 调用 → parseExtractedMemories → 写盘。
//
// 跑法（需要真 ~/.claude 或 ~/.codex 凭据 + 对应 binary 在 PATH）：
//
//	go test -tags=manual -run TestManualAutoDreamE2EPipeline -v ./internal/module/memory/
//
// 对照 B-4.4 dispatcher failover e2e 是 dispatcher 一段，本 test 覆盖
// stop hook 触发段 + consolidator + 写盘段，两者拼起来等同生产路径。
package memory

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/claudecli"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	"github.com/anthropic-ai/super-agent-v3/pkg/dreammetrics"
)

func TestManualAutoDreamE2EPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	dreammetrics.ResetForTesting()
	t.Cleanup(dreammetrics.ResetForTesting)

	// 1. 临时 memory root + 初始 fixture（让 consolidation 有内容可整合）
	root := newTestMemoryRoot(t)
	writeExtractFixture(t, filepath.Join(root, "feedback", "keep-answers-short.md"), testMemoryEntry(
		"Keep answers short",
		"legacy",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: older guidance.",
	))

	// 2. consolidationStamp 设 48h 前，绕过节流
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	if err := recordConsolidation(root, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("recordConsolidation() error = %v", err)
	}

	// 3. 构造真 dispatcher：双 provider failover 链
	providers := []contract.DreamExecutorProvider{
		claudecli.NewDreamExecutorProviderForManualTest(),
		codexapp.NewDreamExecutorProviderForManualTest(),
	}
	dispatcher := unified.NewDreamExecutor(providers, nil)

	// 4. thread store stub：6 sessions + current 共 7 条，过 minSessions=5 阈值
	store := &autoDreamThreadStoreStub{
		thread: newAutoDreamRootThread(t, "thread-e2e", now, map[string]any{"threadKind": "main"}),
		threads: append(
			autoDreamOtherRootThreads(t, now, 6),
			*newAutoDreamRootThread(t, "thread-e2e", now, map[string]any{"threadKind": "main"}),
		),
	}

	// 5. hooks，注入真 dispatcher 作为 extractFn
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil, nil, store, nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	hooks.extractFn = dispatcher.ExecuteDream

	// 6. 触发 dream pipeline（模拟 thread.stopped 事件后的 hooks 调用）
	t.Logf("triggering auto-dream for thread-e2e at %v", now.Format(time.RFC3339))
	started, err := hooks.maybeScheduleAutoDream(ctx, "thread-e2e")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if !started {
		t.Fatal("maybeScheduleAutoDream() = false, want true (sessions ≥ 5 + 48h since last consolidation)")
	}

	// 7. 等 SafeGo 异步任务完成（waitDreamTask 阻塞 done channel）
	waitStart := time.Now()
	if err := hooks.waitDreamTask(ctx); err != nil {
		t.Fatalf("waitDreamTask() error = %v", err)
	}
	waitElapsed := time.Since(waitStart)
	t.Logf("dream task completed in %v", waitElapsed)

	// 8. 验证 consolidation stamp 更新（lastSuccessTime != zero）
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		t.Fatalf("loadConsolidationStamp() error = %v", err)
	}
	lastSuccess := stamp.lastSuccessTime()
	if lastSuccess.IsZero() {
		t.Fatal("lastSuccessTime() = zero, want recorded consolidation success")
	}
	t.Logf("consolidation lastSuccess updated to: %v", lastSuccess.Format(time.RFC3339))

	// 9. 验证 dispatcher metrics 反映真 LLM 调用成功
	snap := dreammetrics.Read()
	t.Logf("dispatcher metrics: %+v", snap)
	if snap.SuccessTotal != 1 {
		t.Errorf("SuccessTotal: got %d, want 1 (dispatcher should record 1 success)", snap.SuccessTotal)
	}
	if snap.AllNotConfiguredTotal != 0 {
		t.Errorf("AllNotConfiguredTotal: got %d, want 0", snap.AllNotConfiguredTotal)
	}
	if got := dreammetrics.TokensInput(); got == 0 {
		t.Errorf("TokensInput() = %d, want > 0 (dream usage should be recorded)", got)
	}

	// 10. 验证 memory entries 真被写到磁盘
	entries, err := scanMemoryEntries(root)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	t.Logf("memory entries after consolidation: %d", len(entries))
	for i, e := range entries {
		t.Logf("  [%d] type=%v canonical=%q content=%q",
			i, e.Frontmatter.Type, e.CanonicalName, truncateForLog(e.Content, 80))
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 memory entry after consolidation, got 0")
	}
}

func truncateForLog(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}
