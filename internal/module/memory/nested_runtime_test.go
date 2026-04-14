package memory

import (
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
)

func TestNestedRuntimeLifecycleResetAndMatcherRoot(t *testing.T) {
	runtime := NewNestedRuntime(&Config{}, nil)
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

	runtime.OnPromptInvalidate(promptpkg.InvalidateProviderSwitch)
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

	runtime.OnPromptInvalidate(promptpkg.InvalidateCompact)
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
	teamRoot := filepath.Join(projectRoot, teamMemoryRootDirName)
	cfg := &Config{RootDir: base, ProjectRoot: projectRoot, AutoMemPathOverride: autoRoot}
	runtime := NewNestedRuntime(cfg, nil)
	manager := NewAgentMemoryManager(cfg)
	agentRoot, err := manager.GetAgentMemoryDir("worker", MemoryScopeProject)
	if err != nil {
		t.Fatalf("GetAgentMemoryDir() error = %v", err)
	}
	buildCtx := contract.BuildCtx{GitRoot: projectRoot, CWD: projectRoot}
	runtime.AddTriggers("thread-1", buildCtx, []string{
		filepath.Join(autoRoot, "project.md"),
		filepath.Join(agentRoot, "MEMORY.md"),
		filepath.Join(teamRoot, "shared.md"),
	})
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending() = %#v, want no managed-memory triggers", got)
	}
}

func TestNestedRuntimeHardDeniesTeamRootWhenKairosActive(t *testing.T) {
	projectRoot := t.TempDir()
	cfg := &Config{
		ProjectRoot: projectRoot,
		Features:    MemoryFeatureFlags{Kairos: true},
	}
	runtime := NewNestedRuntime(cfg, nil)
	buildCtx := contract.BuildCtx{GitRoot: projectRoot, CWD: projectRoot}
	runtime.AddTriggers("thread-1", buildCtx, []string{filepath.Join(projectRoot, teamMemoryRootDirName, "shared.md")})
	if got := runtime.ConsumePending("thread-1", buildCtx); len(got) != 0 {
		t.Fatalf("ConsumePending(kairos team root) = %#v, want none", got)
	}
}

func TestNestedRuntimeResetsForClearWorktreeResumeRestoreAndMemoryWrite(t *testing.T) {
	reasons := []promptpkg.InvalidateReason{
		promptpkg.InvalidateClear,
		promptpkg.InvalidateWorktree,
		promptpkg.InvalidateResumeRestore,
		promptpkg.InvalidateMemoryWrite,
	}
	for _, reason := range reasons {
		t.Run(string(reason), func(t *testing.T) {
			runtime := NewNestedRuntime(&Config{}, nil)
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
	runtime := NewNestedRuntime(&Config{}, nil)
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
	runtime := NewNestedRuntime(&Config{}, nil)
	buildCtx := contract.BuildCtx{GitRoot: "/repo", CWD: "/repo/service"}
	runtime.ObserveBuildContext("thread-1", buildCtx)
	runtime.AddToolReadResult("thread-1", "Read", "Contents of src/main.go:\npackage main", "")
	pending := runtime.ConsumePending("thread-1", buildCtx)
	want := filepath.Join(buildCtx.CWD, "src", "main.go")
	if len(pending) != 1 || pending[0] != want {
		t.Fatalf("ConsumePending(read tool) = %#v, want [%q]", pending, want)
	}
}
