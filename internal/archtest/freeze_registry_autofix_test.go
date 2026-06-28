package archtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanFreezeRegistryAutoFixes(t *testing.T) {
	repoRoot := t.TempDir()
	mkdirFreezeRegistryTestDirs(t, repoRoot, "internal/module/memory", "internal/module/thread")

	// 2026-04-17 默认守卫放宽到 MaxFileLines=600 后，fixture 也需配套抬升，
	// 确保 shrink 分支设计意图（Limit > default 且 default < observed < Limit）仍能被触发。
	const shrinkFreezeLimit = MaxFileLines + 200 // 800
	const shrinkObserved = MaxFileLines + 100    // 700
	const deleteFreezeLimit = MaxFileLines + 50  // 650
	const deleteObserved = MaxFileLines - 200    // 400 <= 600 触发 delete
	registry := []explicitFreeze{
		{
			Path:       "internal/module/memory",
			Kind:       ViolationFile,
			Limit:      shrinkFreezeLimit,
			Reason:     "test shrink",
			Owner:      "test",
			RemoveWhen: "done",
		},
		{
			Path:       "internal/module/thread",
			Kind:       ViolationFile,
			Limit:      deleteFreezeLimit,
			Reason:     "test delete",
			Owner:      "test",
			RemoveWhen: "done",
		},
	}

	stats := map[string]*packageStat{
		"internal/module/memory": {MaxFileLines: shrinkObserved},
		"internal/module/thread": {MaxFileLines: deleteObserved},
	}

	fixes, next := planFreezeRegistryAutoFixesForEntries(repoRoot, []string{"internal"}, stats, registry)
	assertFreezeRegistryFixes(t, fixes, shrinkFreezeLimit, shrinkObserved)
	assertNextFreezeRegistryEntries(t, next, shrinkObserved)
}

func mkdirFreezeRegistryTestDirs(t *testing.T, repoRoot string, dirs ...string) {
	t.Helper()
	for _, rel := range dirs {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", rel, err)
		}
	}
}

func assertFreezeRegistryFixes(t *testing.T, fixes []FreezeRegistryAutoFix, shrinkFreezeLimit, shrinkObserved int) {
	t.Helper()
	if len(fixes) != 2 {
		t.Fatalf("planFreezeRegistryAutoFixes() fixes = %d, want 2", len(fixes))
	}
	wantFirst := FreezeRegistryAutoFix{Path: "internal/module/memory", Kind: "file", Action: "shrink", OldLimit: shrinkFreezeLimit, NewLimit: shrinkObserved, Observed: shrinkObserved, DefaultLimit: MaxFileLines}
	assertFreezeRegistryFix(t, "first", fixes[0], wantFirst)
	wantSecond := FreezeRegistryAutoFix{Path: "internal/module/thread", Kind: "file", Action: "delete", OldLimit: MaxFileLines + 50, Observed: MaxFileLines - 200, DefaultLimit: MaxFileLines}
	assertFreezeRegistryFix(t, "second", fixes[1], wantSecond)
}

func assertFreezeRegistryFix(t *testing.T, label string, got, want FreezeRegistryAutoFix) {
	t.Helper()
	if got != want {
		t.Fatalf("%s fix = %+v, want %+v", label, got, want)
	}
}

func assertNextFreezeRegistryEntries(t *testing.T, next []explicitFreeze, shrinkObserved int) {
	t.Helper()
	if len(next) != 1 {
		t.Fatalf("next entries = %d, want 1", len(next))
	}
	want := explicitFreeze{
		Path:       "internal/module/memory",
		Kind:       ViolationFile,
		Limit:      shrinkObserved,
		Reason:     "test shrink",
		Owner:      "test",
		RemoveWhen: "done",
	}
	if got := next[0]; got != want {
		t.Fatalf("next[0] = %+v, want %+v", got, want)
	}
}
