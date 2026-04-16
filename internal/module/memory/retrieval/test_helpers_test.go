package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

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

func writeTestTopicFile(t *testing.T, path string, entry MemoryEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(formatMemoryEntry(entry)), 0o644); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", path, err)
	}
}

func formatMemoryEntry(entry MemoryEntry) string {
	lines := []string{"---"}
	if name := strings.TrimSpace(entry.Frontmatter.Name); name != "" {
		lines = append(lines, "name: "+strconv.Quote(name))
	}
	if description := strings.TrimSpace(entry.Frontmatter.Description); description != "" {
		lines = append(lines, "description: "+strconv.Quote(description))
	}
	if entry.Frontmatter.Type != nil {
		lines = append(lines, "type: "+strconv.Quote(strings.TrimSpace(string(*entry.Frontmatter.Type))))
	}
	if len(entry.Frontmatter.Aliases) > 0 {
		lines = append(lines, "aliases: "+formatStringList(entry.Frontmatter.Aliases))
	}
	if len(entry.Frontmatter.SearchKeys) > 0 {
		lines = append(lines, "search_keys: "+formatStringList(entry.Frontmatter.SearchKeys))
	}
	lines = append(lines, "---")
	body := strings.TrimSpace(entry.Content)
	if body == "" {
		return strings.Join(lines, "\n") + "\n"
	}
	return strings.Join(lines, "\n") + "\n\n" + body + "\n"
}

func formatStringList(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}
