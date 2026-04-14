package memory

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManifestBuilderBuildManifestScansMemoryFiles(t *testing.T) {
	root := newTestMemoryRoot(t)
	older := testMemoryEntry("Review Style", "Keep review diffs focused", MemoryTypeUser, "Review focused changes first.")
	older.Frontmatter.Aliases = []string{"review preference"}
	newer := testMemoryEntry("Build Guard", "Use guarded build commands", MemoryTypeProject, "Run ./scripts/go_with_guard.sh build ./...")
	newer.Frontmatter.SearchKeys = []string{"build", "guard"}

	olderPath := filepath.Join(root, string(MemoryTypeUser), "review-style.md")
	newerPath := filepath.Join(root, string(MemoryTypeProject), "build-guard.md")
	writeTestTopicFile(t, olderPath, older)
	writeTestTopicFile(t, newerPath, newer)
	writeTestTopicFile(t, memoryIndexPath(root), testMemoryEntry("Index", "should be ignored", MemoryTypeProject, "index"))
	if err := os.WriteFile(filepath.Join(root, "notes.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatalf("WriteFile(notes.txt) error = %v", err)
	}

	olderTime := time.Now().Add(-time.Hour)
	newerTime := time.Now()
	if err := os.Chtimes(olderPath, olderTime, olderTime); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", olderPath, err)
	}
	if err := os.Chtimes(newerPath, newerTime, newerTime); err != nil {
		t.Fatalf("Chtimes(%q) error = %v", newerPath, err)
	}

	builder := NewManifestBuilder()
	manifest, err := builder.BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest() error = %v", err)
	}
	if len(manifest) != 2 {
		t.Fatalf("BuildManifest() entries = %d, want 2", len(manifest))
	}
	if manifest[0].FilePath != newerPath {
		t.Fatalf("BuildManifest()[0].FilePath = %q, want %q", manifest[0].FilePath, newerPath)
	}
	if manifest[0].Content != "" || manifest[1].Content != "" {
		t.Fatalf("BuildManifest() should not preload content, got %#v", manifest)
	}
	if manifest[1].CanonicalName != CanonicalName("Review Style") {
		t.Fatalf("BuildManifest()[1].CanonicalName = %q, want %q", manifest[1].CanonicalName, CanonicalName("Review Style"))
	}
}

func TestManifestBuilderBuildManifestMissingRoot(t *testing.T) {
	builder := NewManifestBuilder()
	manifest, err := builder.BuildManifest(filepath.Join(t.TempDir(), "missing-root"))
	if err != nil {
		t.Fatalf("BuildManifest(missing) error = %v", err)
	}
	if len(manifest) != 0 {
		t.Fatalf("BuildManifest(missing) entries = %d, want 0", len(manifest))
	}
}
