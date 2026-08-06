package team

import (
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestTeamManagerRuntimeGateDefaultsClosed(t *testing.T) {
	autoRoot := filepath.Join(t.TempDir(), "automem")
	manager := NewTeamMemoryManager(newTestConfig(filepath.Join(autoRoot, teamMemoryRootDirName)))
	if manager.IsTeamMemoryEnabled() {
		t.Fatal("IsTeamMemoryEnabled() = true, want false")
	}
	if got := manager.GetTeamMemPath(); got != "" {
		t.Fatalf("GetTeamMemPath() = %q, want empty", got)
	}
	if got := manager.GetTeamMemEntrypoint(); got != "" {
		t.Fatalf("GetTeamMemEntrypoint() = %q, want empty", got)
	}
	if got, err := configuredTeamMemPath(manager); err != nil {
		t.Fatalf("configuredTeamMemPath() error = %v", err)
	} else if want := filepath.Join(autoRoot, teamMemoryRootDirName); got != want {
		t.Fatalf("configuredTeamMemPath() = %q, want %q", got, want)
	}
}

func TestTeamManagerComputesPathWhenRuntimeReady(t *testing.T) {
	autoRoot := filepath.Join(t.TempDir(), "automem")
	manager := NewTeamMemoryManager(newTestConfig(filepath.Join(autoRoot, teamMemoryRootDirName)))
	withTeamMemoryRuntimeReady(t, manager, true)
	wantRoot := filepath.Join(autoRoot, teamMemoryRootDirName)
	if !manager.IsTeamMemoryEnabled() {
		t.Fatal("IsTeamMemoryEnabled() = false, want true")
	}
	if got := manager.GetTeamMemPath(); got != wantRoot {
		t.Fatalf("GetTeamMemPath() = %q, want %q", got, wantRoot)
	}
	if got := manager.GetTeamMemEntrypoint(); got != memoryIndexPath(wantRoot) {
		t.Fatalf("GetTeamMemEntrypoint() = %q, want %q", got, memoryIndexPath(wantRoot))
	}
}

func TestTeamManagerKairosBlocksRuntimePathInjection(t *testing.T) {
	manager := NewTeamMemoryManager(newTestConfig(filepath.Join(t.TempDir(), "automem", teamMemoryRootDirName)))
	withTeamMemoryRuntimeReady(t, manager, true)
	buildCtx := contract.BuildCtx{SessionFlags: map[string]bool{"memory_kairos": true}}
	if manager.IsTeamMemoryEnabled(buildCtx) {
		t.Fatal("IsTeamMemoryEnabled() = true, want false when Kairos is active")
	}
	if got := manager.GetTeamMemPath(buildCtx); got != "" {
		t.Fatalf("GetTeamMemPath() = %q, want empty when Kairos is active", got)
	}
	if got := manager.GetTeamMemEntrypoint(buildCtx); got != "" {
		t.Fatalf("GetTeamMemEntrypoint() = %q, want empty when Kairos is active", got)
	}
}

func TestTeamManagerRuntimeReadinessIsInstanceOwned(t *testing.T) {
	root := filepath.Join(t.TempDir(), "automem", teamMemoryRootDirName)
	ready := NewTeamMemoryManager(newTestConfig(root))
	closed := NewTeamMemoryManager(newTestConfig(root))
	ready.SetRuntimeReady(true)
	if !ready.IsTeamMemoryEnabled() {
		t.Fatal("ready manager IsTeamMemoryEnabled() = false, want true")
	}
	if closed.IsTeamMemoryEnabled() {
		t.Fatal("closed manager IsTeamMemoryEnabled() = true, want false")
	}
}
