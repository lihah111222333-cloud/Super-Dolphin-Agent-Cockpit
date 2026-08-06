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

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/claudecli"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
)

func TestManualAutoDreamE2EPipeline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()

	platformmetrics.DreamRegistry().ResetForTesting()
	t.Cleanup(platformmetrics.DreamRegistry().ResetForTesting)

	root := prepareManualAutoDreamMemoryRoot(t)
	now := time.Date(2026, 4, 15, 9, 0, 0, 0, time.UTC)
	recordManualAutoDreamConsolidation(t, root, now)
	hooks := newManualAutoDreamHooks(t, root, now, manualAutoDreamExecutor())

	runManualAutoDream(t, ctx, hooks, now)
	assertManualAutoDreamStamp(t, root)
	assertManualAutoDreamMetrics(t)
	assertManualAutoDreamEntries(t, root)
}

func prepareManualAutoDreamMemoryRoot(t *testing.T) string {
	t.Helper()

	root := newTestMemoryRoot(t)
	writeExtractFixture(t, filepath.Join(root, "feedback", "keep-answers-short.md"), testMemoryEntry(
		"Keep answers short",
		"legacy",
		MemoryTypeFeedback,
		"Keep answers short\nWhy: older guidance.",
	))
	return root
}

func recordManualAutoDreamConsolidation(t *testing.T, root string, now time.Time) {
	t.Helper()

	if err := recordConsolidation(root, now.Add(-48*time.Hour)); err != nil {
		t.Fatalf("recordConsolidation() error = %v", err)
	}
}

func manualAutoDreamExecutor() contract.DreamExecutor {
	providers := []contract.DreamExecutorProvider{
		claudecli.NewDreamExecutorProviderForManualTest(),
		codexapp.NewDreamExecutorProviderForManualTest(),
	}
	return unified.NewDreamExecutor(providers, nil)
}

func newManualAutoDreamHooks(t *testing.T, root string, now time.Time, dispatcher contract.DreamExecutor) *MemoryLifecycleHooks {
	t.Helper()

	store := &autoDreamThreadStoreStub{
		thread: newAutoDreamRootThread(t, "thread-e2e", now, map[string]any{"threadKind": "main"}),
		threads: append(
			autoDreamOtherRootThreads(t, now, 6),
			*newAutoDreamRootThread(t, "thread-e2e", now, map[string]any{"threadKind": "main"}),
		),
	}
	hooks := newMemoryLifecycleHooks(
		&Config{Enabled: true, RootDir: root},
		NewAutoDreamConsolidator(NewMemoryExtractor()),
		nil, nil, store, nil,
		NewMemoryExtractor(),
		NewManifestBuilder(),
	)
	hooks.timeNow = func() time.Time { return now }
	hooks.extractFn = dispatcher.ExecuteDream
	return hooks
}

func runManualAutoDream(t *testing.T, ctx context.Context, hooks *MemoryLifecycleHooks, now time.Time) {
	t.Helper()

	t.Logf("triggering auto-dream for thread-e2e at %v", now.Format(time.RFC3339))
	started, err := hooks.maybeScheduleAutoDream(ctx, "thread-e2e")
	if err != nil {
		t.Fatalf("maybeScheduleAutoDream() error = %v", err)
	}
	if !started {
		t.Fatal("maybeScheduleAutoDream() = false, want true (sessions ≥ 5 + 48h since last consolidation)")
	}

	waitStart := time.Now()
	if err := hooks.waitDreamTask(ctx); err != nil {
		t.Fatalf("waitDreamTask() error = %v", err)
	}
	t.Logf("dream task completed in %v", time.Since(waitStart))
}

func assertManualAutoDreamStamp(t *testing.T, root string) {
	t.Helper()

	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		t.Fatalf("loadConsolidationStamp() error = %v", err)
	}
	lastSuccess := stamp.lastSuccessTime()
	if lastSuccess.IsZero() {
		t.Fatal("lastSuccessTime() = zero, want recorded consolidation success")
	}
	t.Logf("consolidation lastSuccess updated to: %v", lastSuccess.Format(time.RFC3339))
}

func assertManualAutoDreamMetrics(t *testing.T) {
	t.Helper()

	snap := platformmetrics.DreamRegistry().Read()
	t.Logf("dispatcher metrics: %+v", snap)
	if snap.SuccessTotal != 1 {
		t.Errorf("SuccessTotal: got %d, want 1 (dispatcher should record 1 success)", snap.SuccessTotal)
	}
	if snap.AllNotConfiguredTotal != 0 {
		t.Errorf("AllNotConfiguredTotal: got %d, want 0", snap.AllNotConfiguredTotal)
	}
	if got := platformmetrics.DreamRegistry().TokensInput(); got == 0 {
		t.Errorf("TokensInput() = %d, want > 0 (dream usage should be recorded)", got)
	}
}

func assertManualAutoDreamEntries(t *testing.T, root string) {
	t.Helper()

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
