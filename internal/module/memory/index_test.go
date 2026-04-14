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
