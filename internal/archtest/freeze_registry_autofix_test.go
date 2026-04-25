package archtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanFreezeRegistryAutoFixes(t *testing.T) {
	repoRoot := t.TempDir()

	for _, rel := range []string{
		"internal/module/memory",
		"internal/module/thread",
	} {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", rel, err)
		}
	}

	prev := explicitFreezeRegistry
	// 2026-04-17 默认守卫放宽到 MaxFileLines=600 后，fixture 也需配套抬升，
	// 确保 shrink 分支设计意图（Limit > default 且 default < observed < Limit）仍能被触发。
	const shrinkFreezeLimit = MaxFileLines + 200 // 800
	const shrinkObserved = MaxFileLines + 100    // 700
	const deleteFreezeLimit = MaxFileLines + 50  // 650
	const deleteObserved = MaxFileLines - 200    // 400 <= 600 触发 delete
	explicitFreezeRegistry = []explicitFreeze{
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
	t.Cleanup(func() {
		explicitFreezeRegistry = prev
	})

	stats := map[string]*packageStat{
		"internal/module/memory": {MaxFileLines: shrinkObserved},
		"internal/module/thread": {MaxFileLines: deleteObserved},
	}

	fixes, next := planFreezeRegistryAutoFixes(repoRoot, []string{"internal"}, stats)
	if len(fixes) != 2 {
		t.Fatalf("planFreezeRegistryAutoFixes() fixes = %d, want 2", len(fixes))
	}
	if got := fixes[0]; got.Action != "shrink" || got.Path != "internal/module/memory" || got.OldLimit != shrinkFreezeLimit || got.NewLimit != shrinkObserved {
		t.Fatalf("first fix = %+v, want shrink memory file %d -> %d", got, shrinkFreezeLimit, shrinkObserved)
	}
	if got := fixes[1]; got.Action != "delete" || got.Path != "internal/module/thread" || got.DefaultLimit != MaxFileLines {
		t.Fatalf("second fix = %+v, want delete thread file freeze at default budget", got)
	}
	if len(next) != 1 {
		t.Fatalf("next entries = %d, want 1", len(next))
	}
	if got := next[0]; got.Path != "internal/module/memory" || got.Limit != shrinkObserved {
		t.Fatalf("next[0] = %+v, want memory freeze shrunk to %d", got, shrinkObserved)
	}
}
