package nested

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	memshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
)

func TestNestedRuntimeLifecycleResetAndMatcherRoot(t *testing.T) {
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	repoRoot := filepath.Join(t.TempDir(), "repo")
	buildA := contract.BuildCtx{GitRoot: repoRoot, CWD: filepath.Join(repoRoot, "service")}
	firstTarget := filepath.Join(buildA.CWD, "main.go")
	runtime.AddTriggers("thread-1", buildA, []string{"main.go"})
	assertPendingTrigger(t, runtime, buildA, firstTarget, "ConsumePending()")
	loadedPath := filepath.Join(buildA.CWD, ".claude", "rules", "go.md")
	loaded := ClaudeMdSource{Path: loadedPath, Origin: sourceOriginProject, Type: sourceTypeProject, Digest: "digest-a"}
	if !runtime.MarkLoaded("thread-1", buildA, loaded) {
		t.Fatal("MarkLoaded(first) = false, want true")
	}
	if runtime.MarkLoaded("thread-1", buildA, loaded) {
		t.Fatal("MarkLoaded(second) = true, want false")
	}
	snapshot := runtime.snapshot("thread-1")
	assertNestedSnapshot(t, snapshot, 1, 1, 0, "snapshot after load")

	runtime.OnPromptInvalidate(contract.InvalidateProviderSwitch)
	snapshot = runtime.snapshot("thread-1")
	assertNestedSnapshot(t, snapshot, 1, 1, -1, "provider switch snapshot")

	buildB := contract.BuildCtx{GitRoot: repoRoot, CWD: filepath.Join(repoRoot, "other")}
	secondTarget := filepath.Join(buildB.CWD, "other.go")
	runtime.AddTriggers("thread-1", buildB, []string{"other.go"})
	snapshot = runtime.snapshot("thread-1")
	assertNestedSnapshot(t, snapshot, 2, 0, 1, "snapshot after matcher root change")
	assertPendingTrigger(t, runtime, buildB, secondTarget, "ConsumePending(second)")

	runtime.OnPromptInvalidate(contract.InvalidateCompact)
	snapshot = runtime.snapshot("thread-1")
	assertNestedSnapshot(t, snapshot, 3, 0, 0, "snapshot after compact")

	runtime.OnThreadStart("thread-1")
	snapshot = runtime.snapshot("thread-1")
	assertNestedSnapshot(t, snapshot, 1, 0, 0, "snapshot after thread start")
}

func assertPendingTrigger(t *testing.T, runtime *NestedRuntime, buildCtx contract.BuildCtx, want string, label string) {
	t.Helper()
	pending := runtime.ConsumePending("thread-1", buildCtx)
	if len(pending) != 1 || pending[0] != want {
		t.Fatalf("%s = %#v, want [%q]", label, pending, want)
	}
}

func assertNestedSnapshot(t *testing.T, snapshot nestedSessionState, generation uint64, loadedCount, pendingCount int, label string) {
	t.Helper()
	if snapshot.Generation != generation {
		t.Fatalf("%s = %#v, want generation=%d", label, snapshot, generation)
	}
	if loadedCount >= 0 && len(snapshot.LoadedPaths) != loadedCount {
		t.Fatalf("%s = %#v, want loaded=%d", label, snapshot, loadedCount)
	}
	if pendingCount >= 0 && len(snapshot.PendingTriggers) != pendingCount {
		t.Fatalf("%s = %#v, want pending=%d", label, snapshot, pendingCount)
	}
}

func TestNestedRuntimeHardDeniesManagedRoots(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, "repo")
	autoRoot := filepath.Join(base, "automem")
	teamRoot := filepath.Join(autoRoot, "team")
	deps := newTestDependencies(testDepsOptions{
		autoMemRoot: autoRoot,
		teamRoot:    teamRoot,
	})
	runtime := NewNestedRuntime(deps)
	buildCtx := contract.BuildCtx{GitRoot: projectRoot, CWD: projectRoot}
	runtime.AddTriggers("thread-1", buildCtx, []string{
		filepath.Join(autoRoot, "project.md"),
		filepath.Join(teamRoot, "shared.md"),
	})
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending() = %#v, want no managed-memory triggers", got)
	}
}

func TestNestedRuntimeHardDeniesTeamRootWhenKairosActive(t *testing.T) {
	projectRoot := t.TempDir()
	autoRoot := filepath.Join(t.TempDir(), "automem")
	deps := newTestDependencies(testDepsOptions{autoMemRoot: autoRoot, teamRoot: filepath.Join(autoRoot, "team")})
	runtime := NewNestedRuntime(deps)
	buildCtx := contract.BuildCtx{GitRoot: projectRoot, CWD: projectRoot}
	runtime.AddTriggers("thread-1", buildCtx, []string{filepath.Join(autoRoot, "team", "shared.md")})
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending(kairos team root) = %#v, want none", got)
	}
}

func TestNestedRuntimeResetsForClearWorktreeResumeRestoreAndMemoryWrite(t *testing.T) {
	reasons := []contract.InvalidateReason{
		contract.InvalidateClear,
		contract.InvalidateWorktree,
		contract.InvalidateResumeRestore,
		contract.InvalidateMemoryWrite,
	}
	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
			buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
			runtime.AddTriggers("thread-1", buildCtx, []string{"main.go"})
			_ = runtime.MarkLoaded("thread-1", buildCtx, ClaudeMdSource{Path: "/repo/service/.claude/rules/go.md", Origin: sourceOriginProject, Type: sourceTypeProject, Digest: "digest-a"})
			runtime.OnPromptInvalidate(reason)
			if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
				t.Fatalf("ConsumePending(%s) = %#v, want none after reset", reason, got)
			}
			runtime.AddTriggers("thread-1", buildCtx, []string{"main.go"})
			if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 1 {
				t.Fatalf("ConsumePending(%s after re-add) = %#v, want one trigger", reason, got)
			}
		})
	}
}

func TestNestedRuntimeAllowsReloadWhenDigestChanges(t *testing.T) {
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	sourceA := ClaudeMdSource{Path: "/repo/service/.claude/rules/go.md", Origin: sourceOriginProject, Type: sourceTypeProject, Digest: "digest-a"}
	sourceB := ClaudeMdSource{Path: sourceA.Path, Origin: sourceOriginProject, Type: sourceTypeProject, Digest: "digest-b"}
	if !runtime.MarkLoaded("thread-1", buildCtx, sourceA) {
		t.Fatal("MarkLoaded(digest-a) = false, want true")
	}
	if !runtime.MarkLoaded("thread-1", buildCtx, sourceB) {
		t.Fatal("MarkLoaded(digest-b) = false, want true after digest change")
	}
}

func TestNestedRuntimeAddsReadToolTriggersFromToolResult(t *testing.T) {
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	repoRoot := filepath.Join(t.TempDir(), "repo")
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: filepath.Join(repoRoot, "service")}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	runtime.AddToolReadResult("thread-1", "Read", "Contents of src/main.go:\npackage main", "")
	pending := runtime.ConsumePending("thread-1", buildCtx)
	want := filepath.Join(buildCtx.CWD, "src", "main.go")
	if len(pending) != 1 || pending[0] != want {
		t.Fatalf("ConsumePending(read tool) = %#v, want [%q]", pending, want)
	}
}

func TestNestedRuntimePendingTriggersAreBounded(t *testing.T) {
	t.Parallel()

	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	root := t.TempDir()
	buildCtx := contract.BuildCtx{GitRoot: root, CWD: root}
	triggers := make([]string, 0, 257)
	for i := range 257 {
		triggers = append(triggers, filepath.Join(root, fmt.Sprintf("file-%03d.go", i)))
	}
	err := runtime.AddTriggers("thread-1", buildCtx, triggers)
	if !errors.Is(err, ErrNestedPendingFull) {
		t.Fatalf("AddTriggers(overflow) error = %v, want ErrNestedPendingFull", err)
	}

	snapshot := runtime.snapshot("thread-1")
	if got := len(snapshot.PendingTriggers); got != 0 {
		t.Fatalf("pending trigger count after rejected batch = %d, want 0", got)
	}
}

func TestNestedRuntimeRejectsOversizedPersistedToolOutput(t *testing.T) {
	t.Parallel()

	cacheRoot := t.TempDir()
	repoRoot := filepath.Join(t.TempDir(), "repo")
	cwd := filepath.Join(repoRoot, "service")
	target := filepath.Join(cwd, "main.go")
	persistedPath := filepath.Join(cacheRoot, "tool-output.txt")
	content := "Contents of " + target + ":\n" + strings.Repeat("x", (1<<20)+1)
	if err := os.WriteFile(persistedPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write oversized persisted output: %v", err)
	}

	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	runtime.SetToolReadCacheRoot(cacheRoot)
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: cwd}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	err := runtime.AddToolReadResult("thread-1", "Read", "", persistedPath)
	if !errors.Is(err, memshared.ErrSafeReadTooLarge) {
		t.Fatalf("AddToolReadResult(oversized persisted output) error = %v, want ErrSafeReadTooLarge", err)
	}

	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending(oversized persisted output) = %#v, want rejected output", got)
	}
}

func TestNestedRuntimeThreadStopDeletesState(t *testing.T) {
	t.Parallel()

	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	root := t.TempDir()
	buildCtx := contract.BuildCtx{GitRoot: root, CWD: root}
	runtime.OnThreadStart("thread-stop")
	if err := runtime.AddTriggers("thread-stop", buildCtx, []string{"main.go"}); err != nil {
		t.Fatalf("AddTriggers() error = %v", err)
	}
	runtime.OnThreadStop("thread-stop")

	runtime.mu.Lock()
	_, exists := runtime.sessions[nestedThreadKey("thread-stop")]
	runtime.mu.Unlock()
	if exists {
		t.Fatal("nested session still exists after OnThreadStop")
	}
}

func TestNestedRuntimeSlowReadDoesNotBlockStopOrReviveSession(t *testing.T) {
	t.Parallel()

	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	root := t.TempDir()
	buildCtx := contract.BuildCtx{GitRoot: root, CWD: root}
	runtime.OnThreadStart("thread-slow")
	runtime.ObserveBuildContext("thread-slow", buildCtx)

	started := make(chan struct{})
	release := make(chan struct{})
	runtime.readToolPaths = func(_, _, _, _ string) ([]string, error) {
		close(started)
		<-release
		return []string{filepath.Join(root, "main.go")}, nil
	}
	result := make(chan error, 1)
	runtimesafe.SafeGo(t.Context(), nil, "memory.nested.test.slow-read", func(context.Context) {
		result <- runtime.AddToolReadResult("thread-slow", "Read", "ignored", "")
	})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("AddToolReadResult did not enter injected slow reader")
	}

	stopped := make(chan struct{})
	runtimesafe.SafeGo(t.Context(), nil, "memory.nested.test.thread-stop", func(context.Context) {
		runtime.OnThreadStop("thread-slow")
		close(stopped)
	})
	select {
	case <-stopped:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("OnThreadStop blocked behind slow tool output read")
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("AddToolReadResult() error = %v, want stale result discarded without error", err)
		}
	case <-time.After(time.Second):
		t.Fatal("AddToolReadResult did not return after slow reader release")
	}

	runtime.mu.Lock()
	_, exists := runtime.sessions[nestedThreadKey("thread-slow")]
	runtime.mu.Unlock()
	if exists {
		t.Fatal("slow stale result revived stopped nested session")
	}
}

// TestNestedRuntimePersistedToolReadHonorsCacheRoot 验证持久化 tool read 缓存根的成功路径。
// persistedPath 位于 SetToolReadCacheRoot 下时，shared.SafeReadEntrypoint 可以读取内容并提取 pending trigger。
func TestNestedRuntimePersistedToolReadHonorsCacheRoot(t *testing.T) {
	cacheRoot := t.TempDir()
	repoRoot := filepath.Join(t.TempDir(), "repo")
	cwd := filepath.Join(repoRoot, "service")
	target := filepath.ToSlash(filepath.Join(cwd, "main.go"))
	persistedPath := filepath.Join(cacheRoot, "tool-output.txt")
	if err := os.WriteFile(persistedPath, []byte("Contents of "+target+":\npackage main\n"), 0o600); err != nil {
		t.Fatalf("write persisted tool output: %v", err)
	}
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	runtime.SetToolReadCacheRoot(cacheRoot)
	buildCtx := contract.BuildCtx{GitRoot: repoRoot, CWD: cwd}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	// 空 preview 会强制 helper 读取持久化文件；SafeReadEntrypoint 接线错误时不会产生 trigger。
	if err := runtime.AddToolReadResult("thread-1", "Read", "", persistedPath); err != nil {
		t.Fatalf("AddToolReadResult(persisted in cacheRoot) error = %v", err)
	}
	pending := runtime.ConsumePending("thread-1", buildCtx)
	want := target
	if len(pending) != 1 || filepath.ToSlash(pending[0]) != want {
		t.Fatalf("ConsumePending(persisted in cacheRoot) = %#v, want [%q]", pending, want)
	}
}

// TestNestedRuntimePersistedToolReadRejectsOutsideCacheRoot 验证持久化 tool read 不能越过缓存根。
// persistedPath 解析到 SetToolReadCacheRoot 外部时会被 shared.SafeReadEntrypoint 拒绝，因此不会产生 trigger。
func TestNestedRuntimePersistedToolReadRejectsOutsideCacheRoot(t *testing.T) {
	cacheRoot := t.TempDir()
	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "forged-output.txt")
	if err := os.WriteFile(outsidePath, []byte("Contents of /repo/service/main.go:\npackage main\n"), 0o600); err != nil {
		t.Fatalf("write forged tool output: %v", err)
	}
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	runtime.SetToolReadCacheRoot(cacheRoot)
	buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	err := runtime.AddToolReadResult("thread-1", "Read", "", outsidePath)
	if !errors.Is(err, memshared.ErrSafeReadContainment) {
		t.Fatalf("AddToolReadResult(persisted outside cacheRoot) error = %v, want ErrSafeReadContainment", err)
	}
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending(persisted outside cacheRoot) = %#v, want none (forged path must be rejected)", got)
	}
}

// TestNestedRuntimePersistedToolReadFailsClosedWhenCacheRootUnset 验证未配置缓存根时按关闭处理。
// SetToolReadCacheRoot 从未调用时，即使 persistedPath 指向有效文件也拒绝读取，避免配置缺失时放开无界读。
func TestNestedRuntimePersistedToolReadFailsClosedWhenCacheRootUnset(t *testing.T) {
	dir := t.TempDir()
	persistedPath := filepath.Join(dir, "tool-output.txt")
	if err := os.WriteFile(persistedPath, []byte("Contents of /repo/service/main.go:\npackage main\n"), 0o600); err != nil {
		t.Fatalf("write persisted tool output: %v", err)
	}
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	// 特意不调用 SetToolReadCacheRoot，用来覆盖未配置缓存根的拒绝路径。
	buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	err := runtime.AddToolReadResult("thread-1", "Read", "", persistedPath)
	if !errors.Is(err, ErrNestedToolOutputConfig) {
		t.Fatalf("AddToolReadResult(unset cacheRoot) error = %v, want ErrNestedToolOutputConfig", err)
	}
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending(unset cacheRoot) = %#v, want none (must fail closed)", got)
	}
}

func TestNestedRuntimeHardDeniesHistoricalAgentMemoryRoots(t *testing.T) {
	base := t.TempDir()
	memoryRoot := filepath.Join(base, "memory")
	projectRoot := filepath.Join(base, "repo")
	deps := newTestDependencies(testDepsOptions{autoMemRoot: filepath.Join(memoryRoot, "private")})
	runtime := NewNestedRuntime(deps)
	buildCtx := contract.BuildCtx{GitRoot: projectRoot, CWD: projectRoot}

	runtime.AddTriggers("thread-1", buildCtx, []string{
		filepath.Join(memoryRoot, "agent-memory", "worker", "MEMORY.md"),
		filepath.Join(projectRoot, ".claude", "agent-memory", "worker", "MEMORY.md"),
		filepath.Join(projectRoot, ".claude", "agent-memory-local", "worker", "MEMORY.md"),
	})
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending() = %#v, want no historical agent-memory triggers", got)
	}
}
