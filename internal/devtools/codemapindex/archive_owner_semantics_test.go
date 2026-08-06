package codemapindex

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestArchiveStateCodemapDeclaresThreadOwnedAtomicWrite 锁定归档状态的导航契约：
// thread-owned port，而非 binding store，拥有双表原子写入。
func TestArchiveStateCodemapDeclaresThreadOwnedAtomicWrite(t *testing.T) {
	root := codemapGeneratorRepoRoot(t)

	assertCodemapContains(t, root, "docs/doc/codemap/07-module-write.md",
		"ArchiveStateStore.SetArchiveState",
		"agent_threads.status + agent_provider_binding.archived",
	)
	assertCodemapDoesNotContain(t, root, "docs/doc/codemap/07-module-write.md",
		"SetArchived(true)",
		"SetArchived(false)",
	)
	assertCodemapContains(t, root, "docs/doc/codemap/10-store.md",
		"ArchiveStateStore.SetArchiveState",
		"IMMEDIATE",
		"agent_threads.status + agent_provider_binding.archived",
	)
	assertCodemapDoesNotContain(t, root, "docs/doc/codemap/10-store.md",
		"SetArchived(ctx context.Context, params SetArchivedParams) error",
		"UpdateSessionUUID / SetArchived / UpdateAgentCwd",
	)

	assertCodemapContains(t, root, "internal/module/thread/archive.go", "archiveStateStore.SetArchiveState")
	assertCodemapContains(t, root, "internal/store/thread/archive_state.go", "platformdb.WithImmediateTx", "setArchiveStateInTx")
}

func assertCodemapContains(t *testing.T, root, relative string, want ...string) {
	t.Helper()
	content := readCodemapSemanticFixture(t, root, relative)
	for _, value := range want {
		if !strings.Contains(content, value) {
			t.Fatalf("%s missing required archive owner statement %q", relative, value)
		}
	}
}

func assertCodemapDoesNotContain(t *testing.T, root, relative string, unwanted ...string) {
	t.Helper()
	content := readCodemapSemanticFixture(t, root, relative)
	for _, value := range unwanted {
		if strings.Contains(content, value) {
			t.Fatalf("%s retains obsolete binding-store archive owner statement %q", relative, value)
		}
	}
}

func readCodemapSemanticFixture(t *testing.T, root, relative string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", relative, err)
	}
	return string(content)
}
