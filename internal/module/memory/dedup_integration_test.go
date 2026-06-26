package memory

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteIntentDedupSkipsSameScopeDuplicate(t *testing.T) {
	hooks, autoRoot := newWriteIntentDedupTestHooks(t, false)
	content := "User prefers concise status summaries without filler."
	intent := SaveIntent{Detected: true, Type: MemoryTypeUser, Content: content}

	if err := hooks.writeIntent(context.Background(), "thread-dedup-skip", intent); err != nil {
		t.Fatalf("first writeIntent() error = %v", err)
	}
	entriesBefore := scanEntriesForTest(t, autoRoot)
	indexBefore := readIndexEntries(t, autoRoot)

	if err := hooks.writeIntent(context.Background(), "thread-dedup-skip", intent); err != nil {
		t.Fatalf("second writeIntent() error = %v", err)
	}
	entriesAfter := scanEntriesForTest(t, autoRoot)
	indexAfter := readIndexEntries(t, autoRoot)

	if len(entriesBefore) != 1 {
		t.Fatalf("entries before duplicate write = %d, want 1", len(entriesBefore))
	}
	if len(entriesAfter) != len(entriesBefore) {
		t.Fatalf("entries after duplicate write = %d, want %d", len(entriesAfter), len(entriesBefore))
	}
	if len(indexAfter) != len(indexBefore) {
		t.Fatalf("index entries after duplicate write = %d, want %d", len(indexAfter), len(indexBefore))
	}
	if entriesAfter[0].FilePath != entriesBefore[0].FilePath {
		t.Fatalf("duplicate write changed file path from %q to %q", entriesBefore[0].FilePath, entriesAfter[0].FilePath)
	}
}

func TestWriteIntentDedupMergesNovelSameNameAndUpdatesIndex(t *testing.T) {
	hooks, autoRoot := newWriteIntentDedupTestHooks(t, false)
	first := "User escalation reporting rule.\nKeep status concise and include only verified blockers.\nUse short bullets for decisions.\nMention risk only when it changes the next action."
	second := first + "\nEscalation notes must include owner, deadline, verified next checkpoint, customer impact, rollback condition, monitoring signal, and explicit unblock request."

	if err := hooks.writeIntent(context.Background(), "thread-dedup-merge", SaveIntent{Detected: true, Type: MemoryTypeUser, Content: first}); err != nil {
		t.Fatalf("first writeIntent() error = %v", err)
	}
	entriesBefore := scanEntriesForTest(t, autoRoot)
	if len(entriesBefore) != 1 {
		t.Fatalf("entries before merge = %d, want 1", len(entriesBefore))
	}

	if err := hooks.writeIntent(context.Background(), "thread-dedup-merge", SaveIntent{Detected: true, Type: MemoryTypeUser, Content: second}); err != nil {
		t.Fatalf("second writeIntent() error = %v", err)
	}
	entriesAfter := scanEntriesForTest(t, autoRoot)
	if len(entriesAfter) != 1 {
		t.Fatalf("entries after merge = %d, want 1", len(entriesAfter))
	}
	if entriesAfter[0].FilePath != entriesBefore[0].FilePath {
		t.Fatalf("merge wrote %q, want existing file %q", entriesAfter[0].FilePath, entriesBefore[0].FilePath)
	}
	if !strings.Contains(entriesAfter[0].Content, "Keep status concise") {
		t.Fatalf("merged content lost original body: %q", entriesAfter[0].Content)
	}
	if !strings.Contains(entriesAfter[0].Content, "verified next checkpoint") {
		t.Fatalf("merged content missing novel body: %q", entriesAfter[0].Content)
	}

	indexEntries := readIndexEntries(t, autoRoot)
	if len(indexEntries) != 1 {
		t.Fatalf("index entries after merge = %d, want 1", len(indexEntries))
	}
	if indexEntries[0].Path != indexPathRelForTest(t, autoRoot, entriesAfter[0].FilePath) {
		t.Fatalf("index path = %q, want merged file %q", indexEntries[0].Path, entriesAfter[0].FilePath)
	}
}

func TestWriteIntentDedupCrossScopeDuplicateWritesCurrentScope(t *testing.T) {
	hooks, autoRoot := newWriteIntentDedupTestHooks(t, true)
	teamRoot := filepath.Join(autoRoot, teamMemoryRootDirName)
	teamStore, err := newDiskStore(teamRoot, nil)
	if err != nil {
		t.Fatalf("newDiskStore(team) error = %v", err)
	}
	content := "Team deploy checklist requires rollback owner and release window confirmation."
	teamEntry, err := teamStore.CreateStructured(MemoryWriteRequest{
		Name:        "Existing team-only deploy checklist",
		Description: "team fixture with matching body",
		Type:        MemoryTypeUser,
		Body:        content,
	})
	if err != nil {
		t.Fatalf("CreateStructured(team) error = %v", err)
	}

	if err := hooks.writeIntent(context.Background(), "thread-cross-scope", SaveIntent{Detected: true, Type: MemoryTypeUser, Content: content}); err != nil {
		t.Fatalf("writeIntent() error = %v", err)
	}

	privateEntries := scanPrivateEntriesForTest(t, autoRoot)
	teamEntries := scanEntriesForTest(t, teamRoot)
	if len(privateEntries) != 1 {
		t.Fatalf("private entries = %d, want 1", len(privateEntries))
	}
	if len(teamEntries) != 1 {
		t.Fatalf("team entries = %d, want 1", len(teamEntries))
	}
	if privateEntries[0].FilePath == teamEntry.FilePath {
		t.Fatalf("private write reused team path %q", privateEntries[0].FilePath)
	}
	if !strings.Contains(privateEntries[0].FilePath, autoRoot+string(filepath.Separator)) || strings.Contains(privateEntries[0].FilePath, teamRoot+string(filepath.Separator)) {
		t.Fatalf("private entry path = %q, want under private root %q and outside team root %q", privateEntries[0].FilePath, autoRoot, teamRoot)
	}
	assertIndexContainsPathForTest(t, autoRoot, privateEntries[0].FilePath)

	if len(readIndexEntries(t, teamRoot)) != 1 {
		t.Fatalf("team index should keep the existing cross-scope entry")
	}
}

func newWriteIntentDedupTestHooks(t *testing.T, teamEnabled bool) (*MemoryLifecycleHooks, string) {
	t.Helper()
	if teamEnabled {
		withTeamMemoryRuntimeReady(t, true)
	}
	projectRoot := newTestGitProjectRoot(t)
	autoRoot := filepath.Join(t.TempDir(), "automem")
	cfg := &Config{
		Enabled:             true,
		RootDir:             t.TempDir(),
		ProjectRoot:         projectRoot,
		AutoMemPathOverride: autoRoot,
		Features:            MemoryFeatureFlags{TeamMemory: teamEnabled},
	}
	hooks := NewMemoryLifecycleHooks(memoryLifecycleHookParams{Config: cfg, Team: NewTeamMemoryManager(cfg)})
	if hooks == nil {
		t.Fatal("NewMemoryLifecycleHooks() returned nil")
	}
	if hooks.dedupFilter == nil {
		t.Fatal("NewMemoryLifecycleHooks() did not initialize dedupFilter")
	}
	return hooks, autoRoot
}

func scanEntriesForTest(t *testing.T, root string) []MemoryEntry {
	t.Helper()
	entries, err := scanMemoryEntries(root)
	if err != nil {
		t.Fatalf("scanMemoryEntries(%q) error = %v", root, err)
	}
	return entries
}

func scanPrivateEntriesForTest(t *testing.T, root string) []MemoryEntry {
	t.Helper()
	entries := scanEntriesForTest(t, root)
	teamRoot := filepath.Join(root, teamMemoryRootDirName)
	privateEntries := entries[:0]
	for _, entry := range entries {
		if strings.HasPrefix(entry.FilePath, teamRoot+string(filepath.Separator)) {
			continue
		}
		privateEntries = append(privateEntries, entry)
	}
	return privateEntries
}

func assertIndexContainsPathForTest(t *testing.T, root, path string) {
	t.Helper()
	want := indexPathRelForTest(t, root, path)
	for _, entry := range readIndexEntries(t, root) {
		if entry.Path == want {
			return
		}
	}
	t.Fatalf("index for %q does not contain %q", root, want)
}

func indexPathRelForTest(t *testing.T, root, path string) string {
	t.Helper()
	rel, err := filepath.Rel(root, path)
	if err != nil {
		t.Fatalf("filepath.Rel(%q, %q) error = %v", root, path, err)
	}
	return filepath.ToSlash(rel)
}
