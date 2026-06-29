package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseMemoryIndex(t *testing.T) {
	content := "- [Alpha](user/alpha.md) — first hook\n- [Beta](project/beta.md) — second hook\n"
	entries, err := ParseMemoryIndex(content)
	if err != nil {
		t.Fatalf("ParseMemoryIndex() error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("ParseMemoryIndex() entries = %d, want 2", len(entries))
	}
	if entries[1].CanonicalName != CanonicalName("Beta") {
		t.Fatalf("ParseMemoryIndex() canonical_name = %q, want %q", entries[1].CanonicalName, CanonicalName("Beta"))
	}
}

func TestParseMemoryIndexStripsUTF8BOM(t *testing.T) {
	entries, err := ParseMemoryIndex("\uFEFF- [Alpha](user/alpha.md) — first hook\n")
	if err != nil {
		t.Fatalf("ParseMemoryIndex() with BOM error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("ParseMemoryIndex() with BOM entries = %d, want 1", len(entries))
	}
	if entries[0].Title != "Alpha" {
		t.Fatalf("ParseMemoryIndex() with BOM title = %q, want %q", entries[0].Title, "Alpha")
	}
}

func TestUpdateMemoryIndexDedupesCanonicalName(t *testing.T) {
	root := newTestMemoryRoot(t)
	olderPath := filepath.Join(root, string(MemoryTypeUser), "foo.md")
	newerPath := filepath.Join(root, string(MemoryTypeReference), "foo-copy.md")

	writeTestTopicFile(t, olderPath, testMemoryEntry(" Foo ", "older hook", MemoryTypeUser, "older body"))
	writeTestTopicFile(t, newerPath, testMemoryEntry("foo", "newer hook", MemoryTypeReference, "newer body"))
	olderTime := time.Now().Add(-time.Hour)
	newerTime := time.Now()
	if err := os.Chtimes(olderPath, olderTime, olderTime); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", olderPath, err)
	}
	if err := os.Chtimes(newerPath, newerTime, newerTime); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", newerPath, err)
	}

	entries, err := UpdateMemoryIndex(root)
	if err != nil {
		t.Fatalf("UpdateMemoryIndex() error = %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("UpdateMemoryIndex() entries = %d, want 1", len(entries))
	}
	if entries[0].Hook != "newer hook" {
		t.Fatalf("UpdateMemoryIndex() hook = %q, want newest entry hook", entries[0].Hook)
	}
	if entries[0].Path != filepath.ToSlash(filepath.Join(string(MemoryTypeReference), "foo-copy.md")) {
		t.Fatalf("UpdateMemoryIndex() path = %q, want newest entry path", entries[0].Path)
	}
	parsed := readIndexEntries(t, root)
	if len(parsed) != 1 {
		t.Fatalf("ReadMemoryIndex() entries = %d, want 1", len(parsed))
	}
}

func writeTestTopicFile(t *testing.T, path string, entry MemoryEntry) {
	t.Helper()
	if err := writeAtomicFile(path, []byte(formatMemoryEntry(entry)), 0o644); err != nil {
		t.Fatalf("writeAtomicFile(%q) error = %v", path, err)
	}
}

func TestMemoryFrontmatterSourceRoundTrip(t *testing.T) {
	entry := MemoryEntry{
		Frontmatter: MemoryFrontmatter{
			Name:        "sample dream entry",
			Description: "hook line",
			Source:      "dream",
		},
		Content: "body content",
	}
	encoded := formatMemoryEntry(entry)
	parsed := parseMemoryFrontmatter(encoded)
	if parsed.Source != "dream" {
		t.Fatalf("round-trip Source = %q, want %q", parsed.Source, "dream")
	}
	// 旧文件没有 source 字段时应回落为空。
	legacy := "---\nname: \"legacy\"\ndescription: \"hook\"\n---\n"
	if got := parseMemoryFrontmatter(legacy).Source; got != "" {
		t.Fatalf("legacy frontmatter Source = %q, want empty", got)
	}
}

func TestAgentMemoryIndexHitReturnsIndexReadError(t *testing.T) {
	root := newTestMemoryRoot(t)
	entryPath := filepath.Join(root, string(MemoryTypeUser), "alpha.md")
	entry := testMemoryEntry("Alpha", "hook", MemoryTypeUser, "body")
	writeTestTopicFile(t, entryPath, entry)
	if err := os.Remove(memoryIndexPath(root)); err != nil && !os.IsNotExist(err) {
		t.Fatalf("remove MEMORY.md: %v", err)
	}
	if err := os.Mkdir(memoryIndexPath(root), 0o755); err != nil {
		t.Fatalf("mkdir MEMORY.md sentinel: %v", err)
	}
	entry.FilePath = entryPath

	hit, err := agentMemoryIndexHit(root, entry)
	if err == nil {
		t.Fatalf("agentMemoryIndexHit() hit=%v nil error, want unreadable index error", hit)
	}
	if hit {
		t.Fatal("agentMemoryIndexHit() hit = true on unreadable index")
	}
}
