package memory

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

func canonicalName(raw string) string {
	folded := cases.Fold().String(norm.NFC.String(strings.TrimSpace(raw)))
	return strings.Join(strings.Fields(folded), " ")
}

func scanEntries(root string) ([]diskEntry, error) {
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	entries := make([]diskEntry, 0, 16)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return walkErr
		}
		if filepath.Ext(path) != ".md" || filepath.Base(path) == memoryIndexFileName {
			return nil
		}
		entry, err := readEntryFile(path)
		if err != nil {
			return err
		}
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func readEntryFile(path string) (diskEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return diskEntry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return diskEntry{}, err
	}
	frontmatter, body, ok := splitFrontmatter(string(content))
	entry := contract.MemoryEntry{Content: strings.TrimSpace(body), SourcePath: path, UpdatedAt: info.ModTime()}
	if ok {
		entry = parseFrontmatter(frontmatter, entry)
	} else {
		entry.Content = strings.TrimSpace(string(content))
	}
	entry = normalizeLoadedEntry(entry)
	if entry.Name == "" {
		entry.Name = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}
	return diskEntry{entry: entry, canonicalName: canonicalName(entry.Name), filePath: path}, nil
}

func splitFrontmatter(content string) (string, string, bool) {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if !strings.HasPrefix(content, "---\n") {
		return "", content, false
	}
	frontmatter, tail, ok := strings.Cut(content[4:], "\n---")
	if !ok {
		return "", content, false
	}
	return frontmatter, strings.TrimPrefix(tail, "\n"), true
}

func parseFrontmatter(frontmatter string, entry contract.MemoryEntry) contract.MemoryEntry {
	for line := range strings.SplitSeq(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			entry.Name = parseScalar(value)
		case "description":
			entry.Description = parseScalar(value)
		case "type":
			entry.Type = contract.ParseMemoryType(parseScalar(value))
		}
	}
	return entry
}

func normalizeLoadedEntry(entry contract.MemoryEntry) contract.MemoryEntry {
	entry.Name = strings.Join(strings.Fields(entry.Name), " ")
	entry.Description = strings.Join(strings.Fields(entry.Description), " ")
	entry.Content = strings.TrimSpace(entry.Content)
	entry.Type = contract.ParseMemoryType(string(entry.Type))
	return entry
}

func parseScalar(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var decoded string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return strings.TrimSpace(decoded)
	}
	return strings.Trim(strings.TrimSpace(raw), "\"'")
}

func uniqueEntries(entries []diskEntry) []diskEntry {
	selected := make(map[string]diskEntry, len(entries))
	for _, entry := range entries {
		current, exists := selected[entry.canonicalName]
		if !exists || entry.entry.UpdatedAt.After(current.entry.UpdatedAt) || (entry.entry.UpdatedAt.Equal(current.entry.UpdatedAt) && entry.filePath < current.filePath) {
			selected[entry.canonicalName] = entry
		}
	}
	unique := make([]diskEntry, 0, len(selected))
	for _, entry := range selected {
		unique = append(unique, entry)
	}
	sortEntries(unique)
	return unique
}

func sortEntries(entries []diskEntry) {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].canonicalName == entries[j].canonicalName {
			return entries[i].filePath < entries[j].filePath
		}
		return entries[i].canonicalName < entries[j].canonicalName
	})
}

func hookFromEntry(entry contract.MemoryEntry) string {
	hook := strings.Join(strings.Fields(strings.TrimSpace(entry.Description)), " ")
	if hook == "" {
		hook = firstNonEmptyLine(entry.Content)
	}
	return truncateRunes(hook, memoryHookMaxRunes)
}

func firstNonEmptyLine(text string) string {
	for line := range strings.SplitSeq(text, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
}
