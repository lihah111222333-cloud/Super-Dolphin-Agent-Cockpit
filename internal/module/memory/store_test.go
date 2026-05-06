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
	"unicode/utf8"
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

func TestDiskStoreUpdateStructuredPathRejectsNameMismatchAndKeepsFile(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	created, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Path pinned entry",
		Description: "original hook",
		Type:        MemoryTypeProject,
		Body:        "Original body.\nWhy: original content should survive rejected path update.\nHow to apply: keep the original file untouched.",
	})
	if err != nil {
		t.Fatalf("CreateStructured() error = %v", err)
	}

	_, err = store.UpdateStructuredPath(created.FilePath, MemoryWriteRequest{
		Name:        "Different entry",
		Description: "replacement hook",
		Type:        MemoryTypeProject,
		Body:        "Replacement body.\nWhy: this should be rejected.\nHow to apply: do not write it to the existing path.",
	})
	if !errors.Is(err, ErrInvalidMemoryEntry) {
		t.Fatalf("UpdateStructuredPath(name mismatch) error = %v, want %v", err, ErrInvalidMemoryEntry)
	}
	after, err := readMemoryEntryFile(created.FilePath)
	if err != nil {
		t.Fatalf("readMemoryEntryFile() error = %v", err)
	}
	if after.Frontmatter.Name != created.Frontmatter.Name || strings.Contains(after.Content, "Replacement body") {
		t.Fatalf("entry changed after rejected path update: %#v", after)
	}
}

func TestDiskStoreUpdateStructuredPathRejectsTypeMismatchAndKeepsFile(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	created, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Typed path entry",
		Description: "original hook",
		Type:        MemoryTypeProject,
		Body:        "Original project body.\nWhy: original project content should survive rejected type update.\nHow to apply: keep the project memory typed as project.",
	})
	if err != nil {
		t.Fatalf("CreateStructured() error = %v", err)
	}

	_, err = store.UpdateStructuredPath(created.FilePath, MemoryWriteRequest{
		Name:        "Typed path entry",
		Description: "replacement hook",
		Type:        MemoryTypeUser,
		Body:        "Replacement user body.",
	})
	if !errors.Is(err, ErrInvalidMemoryEntry) {
		t.Fatalf("UpdateStructuredPath(type mismatch) error = %v, want %v", err, ErrInvalidMemoryEntry)
	}
	after, err := readMemoryEntryFile(created.FilePath)
	if err != nil {
		t.Fatalf("readMemoryEntryFile() error = %v", err)
	}
	if after.Type() != MemoryTypeProject || strings.Contains(after.Content, "Replacement user body") {
		t.Fatalf("entry changed after rejected type update: %#v", after)
	}
}

func TestDiskStoreDeletePathRejectsEntrypointIndexAndKeepsFile(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)
	created, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Index delete boundary",
		Description: "delete hook",
		Type:        MemoryTypeUser,
		Body:        "Deleting by path must not remove MEMORY.md.",
	})
	if err != nil {
		t.Fatalf("CreateStructured() error = %v", err)
	}

	if err := store.DeletePath(memoryIndexFileName); !errors.Is(err, ErrInvalidMemoryWritePath) {
		t.Fatalf("DeletePath(MEMORY.md) error = %v, want %v", err, ErrInvalidMemoryWritePath)
	}
	if _, err := os.Stat(memoryIndexPath(root)); err != nil {
		t.Fatalf("MEMORY.md was removed or inaccessible after rejected DeletePath: %v", err)
	}
	if _, err := readMemoryEntryFile(created.FilePath); err != nil {
		t.Fatalf("entry was removed after rejected DeletePath: %v", err)
	}
}

func TestDiskStoreDeletePathRejectsEscapingPathAndKeepsFile(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)

	created, err := store.CreateStructured(MemoryWriteRequest{
		Name:        "Delete path boundary",
		Description: "delete hook",
		Type:        MemoryTypeUser,
		Body:        "Deleting by path must stay inside the memory root.",
	})
	if err != nil {
		t.Fatalf("CreateStructured() error = %v", err)
	}

	if err := store.DeletePath(filepath.Join("..", "escape.md")); !errors.Is(err, ErrInvalidMemoryWritePath) {
		t.Fatalf("DeletePath(escape) error = %v, want %v", err, ErrInvalidMemoryWritePath)
	}
	if _, err := readMemoryEntryFile(created.FilePath); err != nil {
		t.Fatalf("entry was removed after rejected DeletePath: %v", err)
	}
}

func TestWriteMemoryFileLongChineseNameUsesUTF8SafeSlug(t *testing.T) {
	root := newTestMemoryRoot(t)
	name := "用户要求-汇报今日工作-总结今日工作-写日报-时按固定四段简化输出不要包含额外解释"

	written, err := WriteMemoryFile(root, testMemoryEntry(
		name,
		"长中文 memory 名称应生成合法 UTF-8 文件名",
		MemoryTypeFeedback,
		"汇报今日工作时按固定四段简化输出。\nWhy: 用户反复要求固定日报格式。\nHow to apply: 用户要求日报/今日工作总结时按该格式输出。",
	))
	if err != nil {
		t.Fatalf("WriteMemoryFile(long Chinese name) error = %v", err)
	}
	base := filepath.Base(written.FilePath)
	if !utf8.ValidString(base) {
		t.Fatalf("WriteMemoryFile() base path is invalid UTF-8: %q", base)
	}
	if _, err := os.Stat(written.FilePath); err != nil {
		t.Fatalf("Stat(%q) error = %v", written.FilePath, err)
	}
}

func TestWriteConsolidatedMemoriesValidatesBatchBeforeWriting(t *testing.T) {
	root := newTestMemoryRoot(t)
	err := writeConsolidatedMemories(root, []ExtractedMemory{
		{Name: "Valid Topic", Type: MemoryTypeUser, Content: "Keep the valid memory."},
		{Type: MemoryTypeUser},
	})
	if err == nil {
		t.Fatal("writeConsolidatedMemories() error = nil, want validation error")
	}
	entries, scanErr := scanMemoryEntries(root)
	if scanErr != nil {
		t.Fatalf("scanMemoryEntries() error = %v", scanErr)
	}
	if len(entries) != 0 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 0; first = %+v", len(entries), entries[0])
	}
}

func TestWriteConsolidatedMemoriesPersistsDreamSource(t *testing.T) {
	root := newTestMemoryRoot(t)
	if err := writeConsolidatedMemories(root, []ExtractedMemory{
		{Name: "Dream Topic", Type: MemoryTypeProject, Content: "Auto-consolidated content."},
	}); err != nil {
		t.Fatalf("writeConsolidatedMemories() error = %v", err)
	}
	entries, err := scanMemoryEntries(root)
	if err != nil {
		t.Fatalf("scanMemoryEntries() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("scanMemoryEntries() entries = %d, want 1", len(entries))
	}
	if entries[0].Frontmatter.Source != "dream" {
		t.Fatalf("persisted Source = %q, want %q", entries[0].Frontmatter.Source, "dream")
	}
	raw, err := os.ReadFile(entries[0].FilePath)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", entries[0].FilePath, err)
	}
	if !strings.Contains(string(raw), "source: \"dream\"") {
		t.Fatalf("persisted file missing source line; got:\n%s", string(raw))
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

func TestDiskStoreAcceptsChineseStructuredSectionLabels(t *testing.T) {
	root := newTestMemoryRoot(t)
	store := newTestDiskStore(t, root)

	_, err := store.Create(testMemoryEntry(
		"中文结构化模板",
		"中文 UI 模板也应通过结构化校验",
		MemoryTypeProject,
		"事实\n原因：用户界面提供中文模板。\n如何应用：保存时接受中文段落标题。",
	))
	if err != nil {
		t.Fatalf("Create() with Chinese section labels error = %v", err)
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

func newTestDiskStore(t *testing.T, root string) *diskStore {
	t.Helper()
	store, err := newDiskStore(root, nil)
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
