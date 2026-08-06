package archtest

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanFreezeRegistryAutoFixes(t *testing.T) {
	repoRoot := t.TempDir()
	mkdirFreezeRegistryTestDirs(t, repoRoot, "internal/module/memory", "internal/module/thread")

	// 2026-06-28 默认守卫放宽到 MaxFileLines=800 后，fixture 也需配套抬升，
	// 确保 shrink 分支设计意图（Limit > default 且 default < observed < Limit）仍能被触发。
	const shrinkFreezeLimit = MaxFileLines + 200
	const shrinkObserved = MaxFileLines + 100
	const deleteFreezeLimit = MaxFileLines + 50
	const deleteObserved = MaxFileLines - 200
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
	assertFreezeRegistryFixes(t, fixes, shrinkFreezeLimit, shrinkObserved, deleteFreezeLimit, deleteObserved)
	assertNextFreezeRegistryEntries(t, next, shrinkObserved)
}

func TestExplicitFreezeRegistryReturnsIndependentSnapshots(t *testing.T) {
	first := explicitFreezeRegistry()
	second := explicitFreezeRegistry()
	first = append(first, explicitFreeze{Path: "local-only", Kind: ViolationFile})
	if len(second) != 0 {
		t.Fatalf("second freeze registry snapshot length = %d, want 0", len(second))
	}
	if _, ok := frozenLimit("local-only", ViolationFile); ok {
		t.Fatal("local freeze registry mutation leaked into guard lookup")
	}
}

func TestFindExplicitFreezeRegistryOffsetsTargetsSnapshotLiteral(t *testing.T) {
	source := []byte("package archtest\n\nfunc explicitFreezeRegistry() []explicitFreeze {\n\treturn []explicitFreeze{}\n}\n")
	start, end, err := findExplicitFreezeRegistryOffsets("freeze_registry.go", source)
	if err != nil {
		t.Fatalf("find snapshot literal offsets: %v", err)
	}
	if got, want := string(source[start:end]), "[]explicitFreeze{}"; got != want {
		t.Fatalf("snapshot literal = %q, want %q", got, want)
	}
}

// TestPlanFreezeRegistryAutoFixes_Boundary 覆盖 shrinkObserved 恰好等于 MaxFileLines（边界）
// 以及 shrinkObserved == MaxFileLines-1（刚好触发 delete）两个临界路径。
func TestPlanFreezeRegistryAutoFixes_Boundary(t *testing.T) {
	repoRoot := t.TempDir()
	mkdirFreezeRegistryTestDirs(t, repoRoot, "internal/module/alpha", "internal/module/beta")

	const limit = MaxFileLines + 50
	// atDefault: observed == MaxFileLines，shrink 到默认值边界
	// belowDefault: observed == MaxFileLines-1，低于默认值，触发 delete
	registry := []explicitFreeze{
		{Path: "internal/module/alpha", Kind: ViolationFile, Limit: limit, Reason: "r", Owner: "o", RemoveWhen: "w"},
		{Path: "internal/module/beta", Kind: ViolationFile, Limit: limit, Reason: "r", Owner: "o", RemoveWhen: "w"},
	}
	stats := map[string]*packageStat{
		"internal/module/alpha": {MaxFileLines: MaxFileLines},
		"internal/module/beta":  {MaxFileLines: MaxFileLines - 1},
	}

	fixes, _ := planFreezeRegistryAutoFixesForEntries(repoRoot, []string{"internal"}, stats, registry)
	if len(fixes) != 2 {
		t.Fatalf("boundary: fixes = %d, want 2", len(fixes))
	}
	// observed==MaxFileLines 时 freeze 条目已不必要，期望 delete。
	if fixes[0].Action != "delete" {
		t.Errorf("alpha (observed==MaxFileLines): action = %q, want delete", fixes[0].Action)
	}
	if fixes[1].Action != "delete" {
		t.Errorf("beta (observed==MaxFileLines-1): action = %q, want delete", fixes[1].Action)
	}
}

func mkdirFreezeRegistryTestDirs(t *testing.T, repoRoot string, dirs ...string) {
	t.Helper()
	for _, rel := range dirs {
		if err := os.MkdirAll(filepath.Join(repoRoot, filepath.FromSlash(rel)), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", rel, err)
		}
	}
}

func assertFreezeRegistryFixes(t *testing.T, fixes []FreezeRegistryAutoFix, shrinkFreezeLimit, shrinkObserved, deleteFreezeLimit, deleteObserved int) {
	t.Helper()
	if len(fixes) != 2 {
		t.Fatalf("planFreezeRegistryAutoFixes() fixes = %d, want 2", len(fixes))
	}
	wantFirst := FreezeRegistryAutoFix{Path: "internal/module/memory", Kind: "file", Action: "shrink", OldLimit: shrinkFreezeLimit, NewLimit: shrinkObserved, Observed: shrinkObserved, DefaultLimit: MaxFileLines}
	assertFreezeRegistryFix(t, "first", fixes[0], wantFirst)
	wantSecond := FreezeRegistryAutoFix{Path: "internal/module/thread", Kind: "file", Action: "delete", OldLimit: deleteFreezeLimit, Observed: deleteObserved, DefaultLimit: MaxFileLines}
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
