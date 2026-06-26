package memory

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// 本文件固定 combined 模式的 prompt 规则和跨 scope 同名反馈告警。
// 覆盖点包括 feedback/project memory 的专属访问提示、保存前跨 scope 预检查、
// 以及共享 coordinator 对同名告警的一次性去重。
//
// 排名 scope boost 依赖 PrefetchManager 支持双根读取，当前单根 prefetch.go 尚不覆盖，
// 因此这些用例只校验 combined 规则与告警路径。

func TestPhase4_1aCombinedFeedbackAccessLine(t *testing.T) {
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeCombined, true, MemoryRuleOptions{
		AutoMemPath: "/tmp/auto",
		TeamMemPath: "/tmp/team",
	})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(combined) returned nil")
	}
	const want = "next contributor working on this project"
	if !strings.Contains(*out, want) {
		t.Fatalf("combined prompt missing feedback access line %q:\n%s", want, *out)
	}
}

func TestPhase4_1aCombinedProjectAccessLine(t *testing.T) {
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeCombined, true, MemoryRuleOptions{
		AutoMemPath: "/tmp/auto",
		TeamMemPath: "/tmp/team",
	})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(combined) returned nil")
	}
	const want = "flag breaking changes for collaborators"
	if !strings.Contains(*out, want) {
		t.Fatalf("combined prompt missing project access line %q:\n%s", want, *out)
	}
}

func TestPhase4_1aCombinedSaveCrossScopePreCheck(t *testing.T) {
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeCombined, true, MemoryRuleOptions{
		AutoMemPath: "/tmp/auto",
		TeamMemPath: "/tmp/team",
	})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(combined) returned nil")
	}
	const want = "first scan the already-injected"
	if !strings.Contains(*out, want) {
		t.Fatalf("combined prompt missing cross-scope pre-check directive %q:\n%s", want, *out)
	}
}

func TestPhase4_1aStandardModeUnaffected(t *testing.T) {
	// combined-only 的类型提示不能泄漏到 standard 模式。
	// standard 模式保留更窄的 feedback access 文案，用来保护两种 prompt 形状的边界。
	t.Parallel()
	engine := NewMemoryRuleEngine()
	out := engine.LoadMemoryPrompt(MemoryModeStandard, true, MemoryRuleOptions{})
	if out == nil {
		t.Fatal("LoadMemoryPrompt(standard) returned nil")
	}
	for _, leak := range []string{
		"next contributor working on this project",
		"flag breaking changes for collaborators",
		"first scan the already-injected",
	} {
		if strings.Contains(*out, leak) {
			t.Fatalf("standard mode leaked combined-only line %q:\n%s", leak, *out)
		}
	}
}

func TestPhase4_1aWarnCrossScopeSameNameTriggers(t *testing.T) {
	t.Parallel()
	primary, secondary, sharedName := newCrossScopeFixture(t, true /* 两个 store 都有同名条目 */)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hooks := newTestHooks(withLogger(logger))
	hooks.warnCrossScopeSameName(sharedName, primary, primary, secondary, "private", "team")
	got := buf.String()
	if !strings.Contains(got, "memory cross-scope same-name entry detected") {
		t.Fatalf("expected warn log, got:\n%s", got)
	}
	if !strings.Contains(got, sharedName) {
		t.Fatalf("warn log missing name=%q:\n%s", sharedName, got)
	}
	// 结构化字段 selected_scope/other_scope 必须存在，方便运维定位哪个 scope 触发差异。
	if !strings.Contains(got, "selected_scope=private") {
		t.Fatalf("warn log missing selected_scope=private:\n%s", got)
	}
	if !strings.Contains(got, "other_scope=team") {
		t.Fatalf("warn log missing other_scope=team:\n%s", got)
	}
	// 同名条目第二次检查不能再次写 warn，确保 coordinator 去重生效。
	bufBefore := buf.Len()
	hooks.warnCrossScopeSameName(sharedName, primary, primary, secondary, "private", "team")
	if buf.Len() != bufBefore {
		t.Fatalf("dedupe failed: second call re-emitted log:\n%s", buf.String()[bufBefore:])
	}
}

func TestPhase4_1aWarnCrossScopeSameNameNoSecondaryHit(t *testing.T) {
	// 只有选中 store 存在条目时不能告警，避免单侧正常写入被误判为跨 scope 冲突。
	t.Parallel()
	primary, secondary, onlyName := newCrossScopeFixture(t, false /* 只有 primary 有条目 */)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hooks := newTestHooks(withLogger(logger))
	hooks.warnCrossScopeSameName(onlyName, primary, primary, secondary, "private", "team")
	if got := buf.String(); strings.Contains(got, "cross-scope same-name") {
		t.Fatalf("unexpected warn when only primary has entry:\n%s", got)
	}
}

// 并发告警去重测试固定多 goroutine 下只写一条 warn 的行为。
// 多个 goroutine 同时检查同名条目时只能写出一条 warn，防止未来把原子 LoadOrStore 改成非原子检查。
func TestPhase4_1aWarnCrossScopeSameNameConcurrentDedup(t *testing.T) {
	t.Parallel()
	primary, secondary, sharedName := newCrossScopeFixture(t, true)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	hooks := newTestHooks(withLogger(logger))
	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)
	for i := 0; i < concurrency; i++ {
		go func() {
			defer wg.Done()
			hooks.warnCrossScopeSameName(sharedName, primary, primary, secondary, "private", "team")
		}()
	}
	wg.Wait()
	count := strings.Count(buf.String(), "memory cross-scope same-name entry detected")
	if count != 1 {
		t.Fatalf("concurrent dedupe: warn count = %d, want 1\noutput:\n%s", count, buf.String())
	}
}

// 删除路径测试固定 deleteMemoryAcrossStores 不触发跨 scope 同名告警。
// warn helper 只挂在显式写入路径；删除直接走 deleteMemoryAcrossStores，不能污染告警去重状态。

func TestPhase4_1aDeletePathDoesNotWarn(t *testing.T) {
	t.Parallel()
	primary, secondary, sharedName := newCrossScopeFixture(t, true)
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	coordinator := newDiskLockCoordinator()
	_ = newTestHooks(withLogger(logger), withLocks(coordinator))
	if err := deleteMemoryAcrossStores(sharedName, WriteOptions{}, primary, secondary); err != nil {
		t.Fatalf("deleteMemoryAcrossStores() error = %v", err)
	}

	if got := buf.String(); strings.Contains(got, "cross-scope same-name") {
		t.Fatalf("delete path unexpectedly emitted cross-scope warn:\n%s", got)
	}
	if !coordinator.markCrossScopeSameNameWarned(sharedName) {
		t.Fatalf("coordinator cross-scope warn dedupe polluted by delete path: name=%q", sharedName)
	}

}

// newCrossScopeFixture 准备两个互不重叠的磁盘 store。
// bothHave=true 时两个 store 都写入同名条目，否则只写 primary，用于覆盖告警和非告警路径。
func newCrossScopeFixture(t *testing.T, bothHave bool) (memoryStructuredStore, memoryStructuredStore, string) {
	t.Helper()
	primaryRoot := filepath.Join(t.TempDir(), "primary")
	secondaryRoot := filepath.Join(t.TempDir(), "secondary")
	primary, err := newDiskStore(primaryRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(primary) error = %v", err)
	}
	secondary, err := newDiskStore(secondaryRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(secondary) error = %v", err)
	}
	sharedName := "Phase4_1a cross-scope fixture"
	req := MemoryWriteRequest{
		Name:        sharedName,
		Description: "cross-scope warn fixture",
		Type:        MemoryTypeFeedback,
		Body:        "rule\nWhy: drive cross-scope detection.\nHow to apply: warn when both stores carry this entry.",
	}
	if _, err := primary.CreateStructured(req); err != nil {
		t.Fatalf("CreateStructured(primary) error = %v", err)
	}
	if bothHave {
		if _, err := secondary.CreateStructured(req); err != nil {
			t.Fatalf("CreateStructured(secondary) error = %v", err)
		}
	}
	return primary, secondary, sharedName
}
