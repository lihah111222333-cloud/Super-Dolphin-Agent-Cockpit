package retrieval

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRelevantMemoryFinderFindRelevantMemoriesHydratesAndDedupes(t *testing.T) {
	root := newTestMemoryRoot(t)
	primary := testMemoryEntry("Review Style", "Keep review diffs focused", MemoryTypeUser, "Keep diffs small and review focused.")
	primary.Frontmatter.Aliases = []string{"review style"}
	primary.Frontmatter.SearchKeys = []string{"review", "diffs"}
	duplicate := testMemoryEntry("Review Style Copy", "Duplicate copy", MemoryTypeReference, primary.Content)
	duplicate.Frontmatter.SearchKeys = []string{"review"}
	unrelated := testMemoryEntry("Build Guard", "Use guarded build commands", MemoryTypeProject, "Run ./scripts/go_with_guard.sh build ./...")

	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeUser), "review-style.md"), primary)
	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeReference), "review-style-copy.md"), duplicate)
	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeProject), "build-guard.md"), unrelated)

	manifest, err := NewManifestBuilder().BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest() error = %v", err)
	}
	finder := NewRelevantMemoryFinder()
	got, err := finder.FindRelevantMemories(context.Background(), "review diffs", manifest)
	if err != nil {
		t.Fatalf("FindRelevantMemories() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FindRelevantMemories() entries = %d, want 1", len(got))
	}
	if got[0].CanonicalName != CanonicalName("Review Style") {
		t.Fatalf("FindRelevantMemories()[0].CanonicalName = %q, want %q", got[0].CanonicalName, CanonicalName("Review Style"))
	}
	if got[0].Content == "" {
		t.Fatalf("FindRelevantMemories() should hydrate content")
	}
}

func TestRelevantMemoryFinderFindRelevantMemoriesIgnoresMemoryRootPathTerms(t *testing.T) {
	root := filepath.Join(t.TempDir(), "review-root", "memory-root", "project-space")
	primary := testMemoryEntry("Review Style", "Keep review diffs focused", MemoryTypeUser, "Keep diffs small and review focused.")
	unrelated := testMemoryEntry("Build Guard", "Use guarded build commands", MemoryTypeProject, "Run ./scripts/go_with_guard.sh build ./...")

	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeUser), "style.md"), primary)
	writeTestTopicFile(t, filepath.Join(root, string(MemoryTypeProject), "build-guard.md"), unrelated)

	manifest, err := NewManifestBuilder().BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest() error = %v", err)
	}
	finder := NewRelevantMemoryFinder()
	got, err := finder.FindRelevantMemories(context.Background(), "review", manifest)
	if err != nil {
		t.Fatalf("FindRelevantMemories() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FindRelevantMemories() entries = %d, want 1", len(got))
	}
	if got[0].CanonicalName != CanonicalName("Review Style") {
		t.Fatalf("FindRelevantMemories()[0].CanonicalName = %q, want %q", got[0].CanonicalName, CanonicalName("Review Style"))
	}
}

func TestRelevantMemoryFinderFindRelevantMemoriesSkipsFilesDeletedAfterManifest(t *testing.T) {
	root := newTestMemoryRoot(t)
	primaryPath := filepath.Join(root, string(MemoryTypeUser), "review-style.md")
	stalePath := filepath.Join(root, string(MemoryTypeReference), "review-style-stale.md")
	writeTestTopicFile(t, primaryPath, testMemoryEntry("Review Style", "Keep review diffs focused", MemoryTypeUser, "Keep diffs small and review focused."))
	writeTestTopicFile(t, stalePath, testMemoryEntry("Review Style Archive", "Old review guidance", MemoryTypeReference, "Older review guidance."))

	manifest, err := NewManifestBuilder().BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest() error = %v", err)
	}
	wantPath, err := validateMemoryReadPath(root, primaryPath)
	if err != nil {
		t.Fatalf("validateMemoryReadPath(%q) error = %v", primaryPath, err)
	}
	if err := os.Remove(stalePath); err != nil {
		t.Fatalf("Remove(%q) error = %v", stalePath, err)
	}

	finder := NewRelevantMemoryFinder()
	got, err := finder.FindRelevantMemories(context.Background(), "review", manifest)
	if err != nil {
		t.Fatalf("FindRelevantMemories() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FindRelevantMemories() entries = %d, want 1", len(got))
	}
	if got[0].FilePath != wantPath {
		t.Fatalf("FindRelevantMemories()[0].FilePath = %q, want %q", got[0].FilePath, wantPath)
	}
	if got[0].Content == "" {
		t.Fatalf("FindRelevantMemories() should hydrate surviving entry content")
	}
}

func TestRelevantMemoryFinderSelectRelevantMemoriesHonorsBudgetAndDedupes(t *testing.T) {
	finder := NewRelevantMemoryFinder()
	entries := []MemoryEntry{
		{Frontmatter: MemoryFrontmatter{Name: "Alpha"}, FilePath: filepath.Join("memory", "alpha.md"), Content: "abc"},
		{Frontmatter: MemoryFrontmatter{Name: "Alpha Duplicate Path"}, FilePath: filepath.Join("memory", "alpha.md"), Content: "abcdef"},
		{Frontmatter: MemoryFrontmatter{Name: "Alpha Duplicate Content"}, FilePath: filepath.Join("memory", "beta.md"), Content: "abc"},
		{Frontmatter: MemoryFrontmatter{Name: "Gamma"}, FilePath: filepath.Join("memory", "gamma.md"), Content: "12345"},
	}

	got := finder.SelectRelevantMemories(entries, 8)
	if len(got) != 2 {
		t.Fatalf("SelectRelevantMemories() entries = %d, want 2", len(got))
	}
	if got[0].FilePath != filepath.Join("memory", "alpha.md") {
		t.Fatalf("SelectRelevantMemories()[0].FilePath = %q, want alpha.md", got[0].FilePath)
	}
	if got[1].FilePath != filepath.Join("memory", "gamma.md") {
		t.Fatalf("SelectRelevantMemories()[1].FilePath = %q, want gamma.md", got[1].FilePath)
	}
	if len(got[0].Content)+len(got[1].Content) != 8 {
		t.Fatalf("SelectRelevantMemories() budget = %d, want 8", len(got[0].Content)+len(got[1].Content))
	}
}
