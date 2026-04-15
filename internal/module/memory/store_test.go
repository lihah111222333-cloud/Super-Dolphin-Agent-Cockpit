package memory

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

	updated, err := store.Update(testMemoryEntry(" café  preference ", "Prefer focused diffs.", MemoryTypeUser, "Prefer focused diffs.\nWhy: it keeps reviews short.\nHow to apply: land changes in small slices."))
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

func TestDiskStoreReadStripsUTF8BOMFromTopicFile(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	path := filepath.Join(root, string(MemoryTypeUser), "bom-memory.md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	content := "\uFEFF---\nname: BOM Memory\ndescription: Parsed despite BOM\ntype: user\n---\n\nRemember this preference."
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
	entry, err := store.Read("bom memory")
	if err != nil {
		t.Fatalf("Read() with BOM topic error = %v", err)
	}
	if entry.Frontmatter.Name != "BOM Memory" {
		t.Fatalf("Read() with BOM topic name = %q, want %q", entry.Frontmatter.Name, "BOM Memory")
	}
	if entry.Frontmatter.Description != "Parsed despite BOM" {
		t.Fatalf("Read() with BOM topic description = %q, want %q", entry.Frontmatter.Description, "Parsed despite BOM")
	}
	if entry.Content != "Remember this preference." {
		t.Fatalf("Read() with BOM topic content = %q, want %q", entry.Content, "Remember this preference.")
	}
}

func TestDiskStoreReadRemainsStrictWhenRootContainsUnsafeFile(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeUser), "review-style.md"), testMemoryEntry("Review Style", "Keep diffs focused", MemoryTypeUser, "Prefer focused diffs."))

	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "escape.md")
	writeTestTopicFile(t, outsidePath, testMemoryEntry("Escape", "outside root", MemoryTypeReference, "This should never be scanned."))

	linkPath := filepath.Join(root, string(MemoryTypeReference), "escape-link.md")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(linkPath), err)
	}
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}

	if _, err := store.Read("review style"); !errors.Is(err, ErrInvalidMemoryReadPath) {
		t.Fatalf("Read() error = %v, want %v", err, ErrInvalidMemoryReadPath)
	}
}

func TestDiskStoreSkipIndexAndRebuild(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)

	created, err := store.Create(testMemoryEntry("Project Rule", "Keep build commands consistent", MemoryTypeProject, "Use ./scripts/go_with_guard.sh for local builds.\nWhy: local builds should stay aligned with guarded project scripts.\nHow to apply: prefer the guarded wrapper for routine local build commands."), WriteOptions{SkipIndex: true})
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

	_, err := store.Create(testMemoryEntry("Repair Me", "Index failure should not lose topic data", MemoryTypeFeedback, "State the rule clearly.\nWhy: we need an index repair path.\nHow to apply: rebuild the index after restoring the store root."))
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

func TestDiskStoreRejectsStructuredTypesWithoutWhyAndHowToApply(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	_, err := store.Create(testMemoryEntry(
		"Project Rule",
		"Guard build commands",
		MemoryTypeProject,
		"Use guarded build commands in this repo.",
	))
	if !errors.Is(err, ErrInvalidMemoryEntry) {
		t.Fatalf("Create() error = %v, want %v", err, ErrInvalidMemoryEntry)
	}
}

const (
	diskStoreHelperEnv      = "GO_WANT_DISK_STORE_HELPER"
	diskStoreHelperRootEnv  = "DISK_STORE_HELPER_ROOT"
	diskStoreHelperReadyEnv = "DISK_STORE_HELPER_READY"
	diskStoreHelperDoneEnv  = "DISK_STORE_HELPER_DONE"
)

func TestAcquireMemoryRootFileLockCreatesLockFileAndReleases(t *testing.T) {
	root := newTestMemoryRoot(t)
	lockedFile, err := acquireMemoryRootFileLock(root, time.Second)
	if err != nil {
		t.Fatalf("acquireMemoryRootFileLock() error = %v", err)
	}
	lockPath := filepath.Join(root, diskStoreLockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("Stat(%q) error = %v", lockPath, err)
	}
	if err := closeMemoryRootFileLock(lockedFile); err != nil {
		t.Fatalf("closeMemoryRootFileLock() error = %v", err)
	}
	lockedFile, err = acquireMemoryRootFileLock(root, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("re-acquireMemoryRootFileLock() error = %v", err)
	}
	if err := closeMemoryRootFileLock(lockedFile); err != nil {
		t.Fatalf("closeMemoryRootFileLock(reacquired) error = %v", err)
	}
}

func TestDiskStoreCreateWaitsForCrossProcessFileLock(t *testing.T) {
	root := newTestMemoryRoot(t)
	lockedFile, err := acquireMemoryRootFileLock(root, time.Second)
	if err != nil {
		t.Fatalf("acquireMemoryRootFileLock() error = %v", err)
	}
	readyPath := filepath.Join(t.TempDir(), "helper.ready")
	donePath := filepath.Join(t.TempDir(), "helper.done")
	cmd, output := startDiskStoreCreateHelper(t, root, readyPath, donePath)
	waitForTestFile(t, readyPath, time.Second)
	time.Sleep(150 * time.Millisecond)
	if _, err := os.Stat(donePath); !errors.Is(err, os.ErrNotExist) {
		_ = closeMemoryRootFileLock(lockedFile)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		t.Fatalf("helper finished before lock release, err = %v, output = %s", err, output.String())
	}
	if err := closeMemoryRootFileLock(lockedFile); err != nil {
		t.Fatalf("closeMemoryRootFileLock() error = %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper Wait() error = %v, output = %s", err, output.String())
	}
	if _, err := os.Stat(donePath); err != nil {
		t.Fatalf("Stat(%q) error = %v", donePath, err)
	}
	entry, err := newTestDiskStore(t, root).Read("locked topic")
	if err != nil {
		t.Fatalf("Read() after helper Create() error = %v", err)
	}
	if entry.Content != "Write after lock release." {
		t.Fatalf("Read() content = %q, want %q", entry.Content, "Write after lock release.")
	}
}

func TestDiskStoreCreateHelperProcess(t *testing.T) {
	if os.Getenv(diskStoreHelperEnv) != "1" {
		t.Skip("helper process")
	}
	root := os.Getenv(diskStoreHelperRootEnv)
	readyPath := os.Getenv(diskStoreHelperReadyEnv)
	donePath := os.Getenv(diskStoreHelperDoneEnv)
	if err := os.WriteFile(readyPath, []byte("ready"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", readyPath, err)
	}
	store := newTestDiskStore(t, root)
	if _, err := store.Create(testMemoryEntry("Locked Topic", "wait for cross-process lock", MemoryTypeUser, "Write after lock release.")); err != nil {
		t.Fatalf("helper Create() error = %v", err)
	}
	if err := os.WriteFile(donePath, []byte("done"), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", donePath, err)
	}
}

func startDiskStoreCreateHelper(t *testing.T, root, readyPath, donePath string) (*exec.Cmd, *bytes.Buffer) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestDiskStoreCreateHelperProcess$")
	cmd.Env = append(os.Environ(),
		diskStoreHelperEnv+"=1",
		diskStoreHelperRootEnv+"="+root,
		diskStoreHelperReadyEnv+"="+readyPath,
		diskStoreHelperDoneEnv+"="+donePath,
	)
	output := &bytes.Buffer{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatalf("helper Start() error = %v", err)
	}
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, output
}

func waitForTestFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(%q) error = %v", path, err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %q", path)
		}
		time.Sleep(10 * time.Millisecond)
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
