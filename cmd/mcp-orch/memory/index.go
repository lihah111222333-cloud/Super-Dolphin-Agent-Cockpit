package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func loadIndex(root string) ([]indexEntry, bool, string, error) {
	content, err := os.ReadFile(filepath.Join(root, memoryIndexFileName))
	if err == nil {
		parsed, err := parseIndex(root, string(content))
		return parsed, false, "index", err
	}
	entries, scanErr := scanEntries(root)
	if scanErr != nil {
		return nil, true, "rebuilt_view", scanErr
	}
	view, buildErr := buildIndexView(root, entries)
	return view, true, "rebuilt_view", buildErr
}

func parseIndex(root, content string) ([]indexEntry, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	entries := make([]indexEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry, err := parseIndexLine(root, line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func buildIndexView(root string, entries []diskEntry) ([]indexEntry, error) {
	unique := uniqueEntries(entries)
	view := make([]indexEntry, 0, len(unique))
	for _, entry := range unique {
		rel, err := filepath.Rel(root, entry.filePath)
		if err != nil {
			return nil, err
		}
		view = append(view, indexEntry{
			name:          entry.entry.Name,
			description:   entry.entry.Description,
			memoryType:    entry.entry.Type,
			path:          filepath.ToSlash(rel),
			hook:          hookFromEntry(entry.entry),
			canonicalName: entry.canonicalName,
		})
	}
	return view, nil
}

func parseIndexLine(root, line string) (indexEntry, error) {
	if !strings.HasPrefix(line, "- [") {
		return indexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	rest := strings.TrimPrefix(line, "- [")
	title, tail, ok := strings.Cut(rest, "](")
	if !ok {
		return indexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	path, hook, ok := strings.Cut(tail, ")")
	if !ok {
		return indexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	trimmedHook := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(hook), "—"))
	return indexEntry{
		name:          strings.TrimSpace(title),
		description:   trimmedHook,
		memoryType:    readTypeHint(root, path),
		path:          filepath.ToSlash(strings.TrimSpace(path)),
		hook:          trimmedHook,
		canonicalName: canonicalName(title),
	}, nil
}

func readTypeHint(_ string, rel string) contract.MemoryType {
	parts := strings.Split(filepath.ToSlash(rel), "/")
	if len(parts) == 0 {
		return contract.MemoryTypeUnknown
	}
	return contract.ParseMemoryType(parts[0])
}
