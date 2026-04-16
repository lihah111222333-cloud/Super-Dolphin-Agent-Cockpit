package archtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanFreezeRegistryAutoFixes(t *testing.T) {
	t.Parallel()

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
	explicitFreezeRegistry = []explicitFreeze{
		{
			Path:       "internal/module/memory",
			Kind:       ViolationFile,
			Limit:      600,
			Reason:     "test shrink",
			Owner:      "test",
			RemoveWhen: "done",
		},
		{
			Path:       "internal/module/thread",
			Kind:       ViolationFile,
			Limit:      600,
			Reason:     "test delete",
			Owner:      "test",
			RemoveWhen: "done",
		},
	}
	t.Cleanup(func() {
		explicitFreezeRegistry = prev
	})

	stats := map[string]*packageStat{
		"internal/module/memory": {MaxFileLines: 527},
		"internal/module/thread": {MaxFileLines: 398},
	}

	fixes, next := planFreezeRegistryAutoFixes(repoRoot, []string{"internal"}, stats)
	if len(fixes) != 2 {
		t.Fatalf("planFreezeRegistryAutoFixes() fixes = %d, want 2", len(fixes))
	}
	if got := fixes[0]; got.Action != "shrink" || got.Path != "internal/module/memory" || got.OldLimit != 600 || got.NewLimit != 527 {
		t.Fatalf("first fix = %+v, want shrink memory file 600 -> 527", got)
	}
	if got := fixes[1]; got.Action != "delete" || got.Path != "internal/module/thread" || got.DefaultLimit != MaxFileLines {
		t.Fatalf("second fix = %+v, want delete thread file freeze at default budget", got)
	}
	if len(next) != 1 {
		t.Fatalf("next entries = %d, want 1", len(next))
	}
	if got := next[0]; got.Path != "internal/module/memory" || got.Limit != 527 {
		t.Fatalf("next[0] = %+v, want memory freeze shrunk to 527", got)
	}
}
