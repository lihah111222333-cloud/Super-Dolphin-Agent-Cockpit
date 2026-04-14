package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiskStoreCRUD(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)

	created, err := store.Create(testMemoryEntry("  Cafe\u0301\tPreference  ", "Remember the preferred review style", MemoryTypeUser, "Keep diffs focused.\nWhy: smaller reviews are faster.\nHow to apply: split unrelated changes."))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if !strings.Contains(created.FilePath, filepath.Join(root, string(MemoryTypeUser))) {
		t.Fatalf("Create() FilePath = %q, want under %q", created.FilePath, filepath.Join(root, string(MemoryTypeUser)))
	}
	if created.CanonicalName != CanonicalName("CAFÉ preference") {
		t.Fatalf("Create() CanonicalName = %q, want %q", created.CanonicalName, CanonicalName("CAFÉ preference"))
	}

	if _, err := store.Create(testMemoryEntry("café preference", "duplicate", MemoryTypeUser, "duplicate body")); !errors.Is(err, ErrMemoryAlreadyExists) {
		t.Fatalf("duplicate Create() error = %v, want %v", err, ErrMemoryAlreadyExists)
	}

	readEntry, err := store.Read("CAFÉ preference")
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if readEntry.FilePath != created.FilePath {
		t.Fatalf("Read() FilePath = %q, want %q", readEntry.FilePath, created.FilePath)
	}

	updated, err := store.Update(testMemoryEntry(" café  preference ", "", MemoryTypeUser, "Prefer focused diffs.\nWhy: it keeps reviews short.\nHow to apply: land changes in small slices."))
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.FilePath != created.FilePath {
		t.Fatalf("Update() FilePath = %q, want reuse %q", updated.FilePath, created.FilePath)
	}
	if updated.Frontmatter.Description != "Prefer focused diffs." {
		t.Fatalf("Update() Description = %q, want first content line", updated.Frontmatter.Description)
	}

	indexEntries := readIndexEntries(t, root)
	if len(indexEntries) != 1 {
		t.Fatalf("ReadMemoryIndex() entries = %d, want 1", len(indexEntries))
	}
	if indexEntries[0].Hook != "Prefer focused diffs." {
		t.Fatalf("index hook = %q, want updated hook", indexEntries[0].Hook)
	}

	if err := store.Delete("cafe\u0301 preference"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Read("CAFÉ preference"); !errors.Is(err, ErrMemoryNotFound) {
		t.Fatalf("Read() after Delete error = %v, want %v", err, ErrMemoryNotFound)
	}
	content, err := os.ReadFile(memoryIndexPath(root))
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) error = %v", err)
	}
	if string(content) != "" {
		t.Fatalf("MEMORY.md = %q, want empty content", string(content))
	}
}

func TestDiskStoreSkipIndexAndRebuild(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)

	created, err := store.Create(testMemoryEntry("Project Rule", "Keep build commands consistent", MemoryTypeProject, "Use ./scripts/go_with_guard.sh for local builds."), WriteOptions{SkipIndex: true})
	if err != nil {
		t.Fatalf("Create(skipIndex) error = %v", err)
	}
	if _, err := os.Stat(memoryIndexPath(root)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Stat(MEMORY.md) error = %v, want %v", err, os.ErrNotExist)
	}
	if _, err := store.Read("project rule"); err != nil {
		t.Fatalf("Read() after skipIndex create error = %v", err)
	}

	rebuilt, err := store.RebuildIndex()
	if err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}
	if len(rebuilt) != 1 {
		t.Fatalf("RebuildIndex() entries = %d, want 1", len(rebuilt))
	}
	if rebuilt[0].CanonicalName != created.CanonicalName {
		t.Fatalf("RebuildIndex() canonical_name = %q, want %q", rebuilt[0].CanonicalName, created.CanonicalName)
	}
}

func TestDiskStoreIndexUpdateFailureCanBeRepaired(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", root, err)
	}
	if err := os.Mkdir(filepath.Join(root, memoryIndexFileName), 0o755); err != nil {
		t.Fatalf("Mkdir(MEMORY.md) error = %v", err)
	}

	_, err := store.Create(testMemoryEntry("Repair Me", "Index failure should not lose topic data", MemoryTypeFeedback, "State the rule clearly.\nWhy: we need an index repair path."))
	if !errors.Is(err, ErrMemoryIndexUpdateFailed) {
		t.Fatalf("Create() error = %v, want %v", err, ErrMemoryIndexUpdateFailed)
	}
	entries, scanErr := scanMemoryEntries(root)
	if scanErr != nil {
		t.Fatalf("scanMemoryEntries() error = %v", scanErr)
	}
	if len(entries) != 1 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 1", len(entries))
	}

	if err := os.Remove(filepath.Join(root, memoryIndexFileName)); err != nil {
		t.Fatalf("Remove(MEMORY.md directory) error = %v", err)
	}
	rebuilt, err := store.RebuildIndex()
	if err != nil {
		t.Fatalf("RebuildIndex() error = %v", err)
	}
	if len(rebuilt) != 1 {
		t.Fatalf("RebuildIndex() entries = %d, want 1", len(rebuilt))
	}
}

func newTestDiskStore(t *testing.T, root string) *DiskStore {
	t.Helper()
	store, err := NewDiskStore(root)
	if err != nil {
		t.Fatalf("NewDiskStore(%q) error = %v", root, err)
	}
	return store
}

func newTestMemoryRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "memory-root", "project-space")
}

func testMemoryEntry(name, description string, memoryType MemoryType, content string) MemoryEntry {
	return MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        name,
			Description: description,
			Type:        cloneMemoryType(memoryType),
		},
		Content: content,
	}
}

func readIndexEntries(t *testing.T, root string) []MemoryIndexEntry {
	t.Helper()
	entries, err := ReadMemoryIndex(memoryIndexPath(root))
	if err != nil {
		t.Fatalf("ReadMemoryIndex(%q) error = %v", memoryIndexPath(root), err)
	}
	return entries
}
