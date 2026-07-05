package retrieval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestManifestBuilderBuildManifestScansMemoryFiles(t *testing.T) {
	root := newTestMemoryRoot(t)
	_, newerPath := writeManifestScanFixtures(t, root)

	manifest := buildManifestForTest(t, root)
	wantNewerPath := validateReadPathForTest(t, root, newerPath)
	assertScannedManifest(t, manifest, wantNewerPath)
}

func writeManifestScanFixtures(t *testing.T, root string) (string, string) {
	t.Helper()
	older := testMemoryEntry("Review Style", "Keep review diffs focused", MemoryTypeUser, "Review focused changes first.")
	older.Frontmatter.Aliases = []string{"review preference"}
	newer := testMemoryEntry("Build Guard", "Use guarded build commands", MemoryTypeProject, "Run ./scripts/go_with_guard.sh build ./...")
	newer.Frontmatter.SearchKeys = []string{"build", "guard"}
	newer.Frontmatter.Title = "Guard"

	olderPath := filepath.Join(root, string(MemoryTypeUser), "review-style.md")
	newerPath := filepath.Join(root, string(MemoryTypeProject), "build-guard.md")
	writeTestTopicFile(t, olderPath, older)
	writeTestTopicFile(t, newerPath, newer)
	writeTestTopicFile(t, filepath.Join(root, memoryIndexFileName), testMemoryEntry("Index", "should be ignored", MemoryTypeProject, "index"))
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
	return olderPath, newerPath
}

func buildManifestForTest(t *testing.T, root string) []MemoryEntry {
	t.Helper()
	manifest, err := NewManifestBuilder().BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest() error = %v", err)
	}
	return manifest
}

func validateReadPathForTest(t *testing.T, root, path string) string {
	t.Helper()
	wantPath, err := validateMemoryReadPath(root, path)
	if err != nil {
		t.Fatalf("validateMemoryReadPath(%q) error = %v", path, err)
	}
	return wantPath
}

func assertScannedManifest(t *testing.T, manifest []MemoryEntry, wantNewerPath string) {
	t.Helper()
	if len(manifest) != 2 {
		t.Fatalf("BuildManifest() entries = %d, want 2", len(manifest))
	}
	if manifest[0].FilePath != wantNewerPath {
		t.Fatalf("BuildManifest()[0].FilePath = %q, want %q", manifest[0].FilePath, wantNewerPath)
	}
	if manifest[0].Content != "" || manifest[1].Content != "" {
		t.Fatalf("BuildManifest() should not preload content, got %#v", manifest)
	}
	if manifest[0].Frontmatter.Title != "Guard" {
		t.Fatalf("BuildManifest()[0].Frontmatter.Title = %q, want %q", manifest[0].Frontmatter.Title, "Guard")
	}
	if manifest[1].CanonicalName != CanonicalName("Review Style") {
		t.Fatalf("BuildManifest()[1].CanonicalName = %q, want %q", manifest[1].CanonicalName, CanonicalName("Review Style"))
	}
}

func TestScanHeadersSafeAndBuildManifestSkipUnsafeFiles(t *testing.T) {
	root := newTestMemoryRoot(t)
	insidePath := writeSafeAndUnsafeManifestFixtures(t, root)
	wantPath := validateReadPathForTest(t, root, insidePath)

	headers := scanHeadersForTest(t, root)
	assertSingleHeaderPath(t, headers, wantPath)
	assertSingleManifestPath(t, buildManifestForTest(t, root), wantPath)
}

func writeSafeAndUnsafeManifestFixtures(t *testing.T, root string) string {
	t.Helper()
	insidePath := filepath.Join(root, string(MemoryTypeUser), "inside.md")
	writeTestTopicFile(t, insidePath, testMemoryEntry("Inside", "safe entry", MemoryTypeUser, "Keep this entry."))

	outsideRoot := t.TempDir()
	outsidePath := filepath.Join(outsideRoot, "outside.md")
	writeTestTopicFile(t, outsidePath, testMemoryEntry("Outside", "should be skipped", MemoryTypeReference, "Do not load me."))

	linkPath := filepath.Join(root, string(MemoryTypeReference), "outside-link.md")
	if err := os.MkdirAll(filepath.Dir(linkPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(linkPath), err)
	}
	if err := os.Symlink(outsidePath, linkPath); err != nil {
		t.Skipf("Symlink unsupported: %v", err)
	}
	return insidePath
}

func scanHeadersForTest(t *testing.T, root string) []MemoryEntry {
	t.Helper()
	headers, err := ScanHeadersSafe(root)
	if err != nil {
		t.Fatalf("ScanHeadersSafe() error = %v", err)
	}
	return headers
}

func assertSingleHeaderPath(t *testing.T, headers []MemoryEntry, wantPath string) {
	t.Helper()
	if len(headers) != 1 {
		t.Fatalf("ScanHeadersSafe() entries = %d, want 1", len(headers))
	}
	if headers[0].FilePath != wantPath {
		t.Fatalf("ScanHeadersSafe()[0].FilePath = %q, want %q", headers[0].FilePath, wantPath)
	}
	if headers[0].Content != "" {
		t.Fatalf("ScanHeadersSafe() should not preload content, got %q", headers[0].Content)
	}
}

func assertSingleManifestPath(t *testing.T, manifest []MemoryEntry, wantPath string) {
	t.Helper()
	if len(manifest) != 1 {
		t.Fatalf("BuildManifest() entries = %d, want 1", len(manifest))
	}
	if manifest[0].FilePath != wantPath {
		t.Fatalf("BuildManifest()[0].FilePath = %q, want %q", manifest[0].FilePath, wantPath)
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

// TestBuildManifestStopsWalkingAtMaxFiles verifies the scan budget stops before reading the next file.
func TestBuildManifestStopsWalkingAtMaxFiles(t *testing.T) {
	root := newTestMemoryRoot(t)
	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeUser), "0001-valid.md"), testMemoryEntry("One", "first", MemoryTypeUser, "one"))
	writeOversizedHeaderFile(t, filepath.Join(root, string(MemoryTypeUser), "0002-broken.md"))

	builder := NewManifestBuilder()
	builder.MaxFiles = 1
	manifest, err := builder.BuildManifest(root)
	if err == nil || !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("BuildManifest() error = %v, want max-files truncation before reading second file", err)
	}
	if len(manifest) != 1 || manifest[0].Frontmatter.Name != "One" {
		t.Fatalf("BuildManifest() entries = %#v, want first valid entry retained", manifest)
	}
}

// TestScanHeadersSafeReturnsReadError verifies header scan failures are reported instead of skipped.
func TestScanHeadersSafeReturnsReadError(t *testing.T) {
	root := newTestMemoryRoot(t)
	brokenPath := filepath.Join(root, string(MemoryTypeUser), "broken.md")
	writeOversizedHeaderFile(t, brokenPath)

	headers, err := ScanHeadersSafe(root)
	if err == nil || !strings.Contains(err.Error(), "broken.md") {
		t.Fatalf("ScanHeadersSafe() error = %v entries=%#v, want typed read/header error", err, headers)
	}
}

func writeOversizedHeaderFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	content := "---\nname: " + strings.Repeat("x", manifestHeaderScanLimit+1) + "\n---\nbody\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}
