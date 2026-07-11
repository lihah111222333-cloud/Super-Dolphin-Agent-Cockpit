package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	parse "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/parse"
	retrievalpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/retrieval"
)

func TestStripHTMLCommentsRemovesBlockCommentsAndKeepsCodeFences(t *testing.T) {
	content := strings.Join([]string{
		"Keep this.",
		"<!-- remove me -->",
		"Use <!-- inline --> comments carefully.",
		"```md",
		"<!-- keep in fence -->",
		"@./ignore.md",
		"```",
		"<!-- multi",
		"line -->",
		"<!-- note -->Keep that.",
		"",
	}, "\n")

	got := parse.StripHTMLComments(content)
	want := strings.Join([]string{
		"Keep this.",
		"Use <!-- inline --> comments carefully.",
		"```md",
		"<!-- keep in fence -->",
		"@./ignore.md",
		"```",
		"Keep that.",
		"",
	}, "\n")
	if got != want {
		t.Fatalf("StripHTMLComments() = %q, want %q", got, want)
	}
}

func TestStripHTMLCommentsKeepsUnclosedComment(t *testing.T) {
	content := "Keep\n<!-- unclosed\nstill here\n"
	if got := parse.StripHTMLComments(content); got != content {
		t.Fatalf("StripHTMLComments() with unclosed comment = %q, want original %q", got, content)
	}
}

func TestExtractIncludesFindsSupportedPathsOutsideCodeFences(t *testing.T) {
	content := strings.Join([]string{
		"@./alpha.md",
		"See @~/team\\ memory.md and @/workspace/docs.md#intro",
		"```md",
		"@./ignore.md",
		"```",
		"@./alpha.md",
		"",
	}, "\n")

	got := ExtractIncludes(content)
	want := []string{"./alpha.md", "~/team memory.md", "/workspace/docs.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExtractIncludes() = %#v, want %#v", got, want)
	}
}

func TestParseMemoryFilePreservesRawContentWhenContentChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "topic.md")
	raw := "\uFEFF---\nname: Review Style\ndescription: Keep review diffs focused\ntype: user\ntitle: Review\n---\n\n<!-- hidden -->\nKeep diffs small.\n@./details.md\n"

	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	parsed, err := ParseMemoryFile(path)
	if err != nil {
		t.Fatalf("ParseMemoryFile() error = %v", err)
	}
	if strings.TrimSpace(parsed.Content) != "Keep diffs small.\n@./details.md" {
		t.Fatalf("ParseMemoryFile() content = %q", parsed.Content)
	}
	if !parsed.ContentDiffersFromDisk {
		t.Fatalf("ParseMemoryFile() ContentDiffersFromDisk = false, want true")
	}
	if parsed.RawContent != raw {
		t.Fatalf("ParseMemoryFile() RawContent = %q, want %q", parsed.RawContent, raw)
	}
	if parsed.Frontmatter.Name != "Review Style" {
		t.Fatalf("ParseMemoryFile() frontmatter name = %q, want %q", parsed.Frontmatter.Name, "Review Style")
	}
	if parsed.Frontmatter.Title != "Review" {
		t.Fatalf("ParseMemoryFile() frontmatter title = %q, want %q", parsed.Frontmatter.Title, "Review")
	}
	if !reflect.DeepEqual(parsed.Includes, []string{"./details.md"}) {
		t.Fatalf("ParseMemoryFile() includes = %#v, want %#v", parsed.Includes, []string{"./details.md"})
	}
}

func TestParseMemoryFileTruncatesEntrypointContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), memoryIndexFileName)
	lines := make([]string, entrypointMaxLines+5)
	for i := range lines {
		lines[i] = fmt.Sprintf("line %03d", i)
	}
	raw := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(raw), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	parsed, err := ParseMemoryFile(path)
	if err != nil {
		t.Fatalf("ParseMemoryFile() error = %v", err)
	}
	if !strings.Contains(parsed.Content, "> WARNING: MEMORY.md is") {
		t.Fatalf("ParseMemoryFile() content missing truncation warning: %q", parsed.Content)
	}
	if strings.Contains(parsed.Content, fmt.Sprintf("line %03d", entrypointMaxLines)) {
		t.Fatalf("ParseMemoryFile() content still contains discarded line: %q", parsed.Content)
	}
	if !parsed.ContentDiffersFromDisk || parsed.RawContent != raw {
		t.Fatalf("ParseMemoryFile() raw preservation = %+v, want original content retained", parsed)
	}
}

func TestRelevantMemoryFinderHydrateUsesParsedMemoryContent(t *testing.T) {
	root := newTestMemoryRoot(t)
	path := filepath.Join(root, string(MemoryTypeUser), "review-style.md")
	content := "---\nname: Review Style\ndescription: Keep review diffs focused\ntype: user\n---\n\n<!-- hidden -->\nKeep diffs small and review focused.\n@./details.md\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}

	manifest, err := NewManifestBuilder().BuildManifest(root)
	if err != nil {
		t.Fatalf("BuildManifest() error = %v", err)
	}
	got, err := retrievalpkg.NewRelevantMemoryFinder().FindRelevantMemories(context.Background(), "review diffs", manifest)
	if err != nil {
		t.Fatalf("FindRelevantMemories() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("FindRelevantMemories() entries = %d, want 1", len(got))
	}
	if strings.Contains(got[0].Content, "hidden") {
		t.Fatalf("FindRelevantMemories() content still contains stripped HTML comment: %q", got[0].Content)
	}
	if !strings.Contains(got[0].Content, "Keep diffs small and review focused.") {
		t.Fatalf("FindRelevantMemories() content = %q, want hydrated body", got[0].Content)
	}
}
