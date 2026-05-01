package nested

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestNestedRuntimeLifecycleResetAndMatcherRoot(t *testing.T) {
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	buildA := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	firstTarget := filepath.Join(buildA.CWD, "main.go")
	runtime.AddTriggers("thread-1", buildA, []string{"main.go"})
	pending := runtime.ConsumePending("thread-1", buildA)
	if len(pending) != 1 || pending[0] != firstTarget {
		t.Fatalf("ConsumePending() = %#v, want [%q]", pending, firstTarget)
	}
	loadedPath := filepath.Join(buildA.CWD, ".claude", "rules", "go.md")
	loaded := ClaudeMdSource{Path: loadedPath, Origin: sourceOriginProject, Type: sourceTypeProject, Digest: "digest-a"}
	if !runtime.MarkLoaded("thread-1", buildA, loaded) {
		t.Fatal("MarkLoaded(first) = false, want true")
	}
	if runtime.MarkLoaded("thread-1", buildA, loaded) {
		t.Fatal("MarkLoaded(second) = true, want false")
	}
	snapshot := runtime.snapshot("thread-1")
	if snapshot.Generation != 1 || len(snapshot.LoadedPaths) != 1 || len(snapshot.PendingTriggers) != 0 {
		t.Fatalf("snapshot after load = %#v, want generation=1 loaded=1 pending=0", snapshot)
	}

	runtime.OnPromptInvalidate(contract.InvalidateProviderSwitch)
	snapshot = runtime.snapshot("thread-1")
	if snapshot.Generation != 1 || len(snapshot.LoadedPaths) != 1 {
		t.Fatalf("provider switch snapshot = %#v, want unchanged generation+loaded", snapshot)
	}

	buildB := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/other"}
	secondTarget := filepath.Join(buildB.CWD, "other.go")
	runtime.AddTriggers("thread-1", buildB, []string{"other.go"})
	snapshot = runtime.snapshot("thread-1")
	if snapshot.Generation != 2 || len(snapshot.LoadedPaths) != 0 || len(snapshot.PendingTriggers) != 1 {
		t.Fatalf("snapshot after matcher root change = %#v, want generation=2 cleared loaded with one pending", snapshot)
	}
	pending = runtime.ConsumePending("thread-1", buildB)
	if len(pending) != 1 || pending[0] != secondTarget {
		t.Fatalf("ConsumePending(second) = %#v, want [%q]", pending, secondTarget)
	}

	runtime.OnPromptInvalidate(contract.InvalidateCompact)
	snapshot = runtime.snapshot("thread-1")
	if snapshot.Generation != 3 || len(snapshot.LoadedPaths) != 0 || len(snapshot.PendingTriggers) != 0 {
		t.Fatalf("snapshot after compact = %#v, want generation=3 and cleared state", snapshot)
	}

	runtime.OnThreadStart("thread-1")
	snapshot = runtime.snapshot("thread-1")
	if snapshot.Generation != 1 || len(snapshot.LoadedPaths) != 0 || len(snapshot.PendingTriggers) != 0 {
		t.Fatalf("snapshot after thread start = %#v, want fresh generation=1", snapshot)
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
	buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	runtime.AddToolReadResult("thread-1", "Read", "Contents of src/main.go:\npackage main", "")
	pending := runtime.ConsumePending("thread-1", buildCtx)
	want := filepath.Join(buildCtx.CWD, "src", "main.go")
	if len(pending) != 1 || pending[0] != want {
		t.Fatalf("ConsumePending(read tool) = %#v, want [%q]", pending, want)
	}
}

// TestNestedRuntimePersistedToolReadHonorsCacheRoot verifies the P24
// cache-root-threading happy path: a persistedPath under the configured
// SetToolReadCacheRoot is read via shared.SafeReadEntrypoint and its
// `Contents of <path>:` line surfaces as a pending trigger.
func TestNestedRuntimePersistedToolReadHonorsCacheRoot(t *testing.T) {
	cacheRoot := t.TempDir()
	persistedPath := filepath.Join(cacheRoot, "tool-output.txt")
	if err := os.WriteFile(persistedPath, []byte("Contents of /repo/service/main.go:\npackage main\n"), 0o600); err != nil {
		t.Fatalf("write persisted tool output: %v", err)
	}
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	runtime.SetToolReadCacheRoot(cacheRoot)
	buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	// Empty preview forces the helper to fall back to the persisted file; if
	// the SafeReadEntrypoint plumbing is wrong the test fails because no
	// trigger is produced.
	runtime.AddToolReadResult("thread-1", "Read", "", persistedPath)
	pending := runtime.ConsumePending("thread-1", buildCtx)
	want := "/repo/service/main.go"
	if len(pending) != 1 || pending[0] != want {
		t.Fatalf("ConsumePending(persisted in cacheRoot) = %#v, want [%q]", pending, want)
	}
}

// TestNestedRuntimePersistedToolReadRejectsOutsideCacheRoot verifies the P24
// cache-root-threading containment guarantee: a persistedPath that resolves
// outside SetToolReadCacheRoot is rejected by shared.SafeReadEntrypoint and
// the helper falls back to the in-memory preview (here empty), so no trigger
// surfaces.
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
	runtime.AddToolReadResult("thread-1", "Read", "", outsidePath)
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending(persisted outside cacheRoot) = %#v, want none (forged path must be rejected)", got)
	}
}

// TestNestedRuntimePersistedToolReadFailsClosedWhenCacheRootUnset verifies the
// P24 fail-closed contract: if SetToolReadCacheRoot was never called the
// helper refuses to read any persistedPath even when the file would be
// otherwise valid, so a misconfigured deployment cannot accidentally re-open
// the unbounded read.
func TestNestedRuntimePersistedToolReadFailsClosedWhenCacheRootUnset(t *testing.T) {
	dir := t.TempDir()
	persistedPath := filepath.Join(dir, "tool-output.txt")
	if err := os.WriteFile(persistedPath, []byte("Contents of /repo/service/main.go:\npackage main\n"), 0o600); err != nil {
		t.Fatalf("write persisted tool output: %v", err)
	}
	runtime := NewNestedRuntime(newTestDependencies(testDepsOptions{}))
	// Intentionally do NOT call SetToolReadCacheRoot.
	buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	runtime.AddToolReadResult("thread-1", "Read", "", persistedPath)
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending(unset cacheRoot) = %#v, want none (must fail closed)", got)
	}
}
