package retrieval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
)

const manifestHeaderScanLimit = 32 * 1024

func readMemoryEntryHeader(path string) (MemoryEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	defer file.Close()

	header, err := parse.ScanFrontmatterHeader(file, manifestHeaderScanLimit)
	if err != nil {
		return MemoryEntry{}, err
	}
	info, err := file.Stat()
	if err != nil {
		return MemoryEntry{}, err
	}
	entry := parseMemoryHeader(path, header)
	entry.FilePath = path
	entry.UpdatedAt = info.ModTime()
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return normalizeLoadedEntry(entry), nil
}

func readMemoryEntryFile(path string) (MemoryEntry, error) {
	rawContent, err := os.ReadFile(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	content := parse.StripUTF8BOM(string(rawContent))
	entry := MemoryEntry{Content: content, FilePath: path, UpdatedAt: info.ModTime()}
	if frontmatter, body, ok := parse.SplitFrontmatter(content); ok {
		entry.Frontmatter = parseMemoryFrontmatter(frontmatter)
		entry.Content = body
	}
	entry.Content = parse.StripHTMLComments(entry.Content)
	entry = normalizeLoadedEntry(entry)
	if entry.Frontmatter.Name == "" {
		entry.Frontmatter.Name = fallbackEntryName(path)
	}
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return entry, nil
}

func parseMemoryHeader(path, header string) MemoryEntry {
	frontmatter, _, ok := parse.SplitFrontmatter(header)
	if !ok {
		return MemoryEntry{FilePath: path}
	}
	return MemoryEntry{Frontmatter: parseMemoryFrontmatter(frontmatter), FilePath: path}
}

// parseMemoryFrontmatter 解析记忆frontmatter。
func parseMemoryFrontmatter(frontmatter string) MemoryFrontmatter {
	parsed := MemoryFrontmatter{}
	for _, line := range strings.Split(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		switch key {
		case "name":
			parsed.Name = parseScalar(value)
		case "description":
			parsed.Description = parseScalar(value)
		case "type":
			t := ParseMemoryType(parseScalar(value))
			parsed.Type = cloneMemoryType(t)
		case "lang":
			parsed.Lang = parseScalar(value)
		case "aliases":
			parsed.Aliases = parseStringList(value)
		case "search_keys":
			parsed.SearchKeys = parseStringList(value)
		case "title", "source":
			assignMemoryFrontmatterScalar(&parsed, key, value)
		}
	}
	return parsed
}

func assignMemoryFrontmatterScalar(parsed *MemoryFrontmatter, key, value string) {
	value = parseScalar(value)
	switch key {
	case "title":
		parsed.Title = value
	case "source":
		parsed.Source = value
	}
}

func normalizeLoadedEntry(entry MemoryEntry) MemoryEntry {
	entry.Frontmatter.Name = strings.Join(strings.Fields(entry.Frontmatter.Name), " ")
	entry.Frontmatter.Description = strings.Join(strings.Fields(entry.Frontmatter.Description), " ")
	entry.Frontmatter.Lang = strings.TrimSpace(entry.Frontmatter.Lang)
	entry.Frontmatter.Aliases = normalizeStringSlice(entry.Frontmatter.Aliases)
	entry.Frontmatter.SearchKeys = normalizeStringSlice(entry.Frontmatter.SearchKeys)
	if entry.Frontmatter.Type != nil {
		entry.Frontmatter.Type = cloneMemoryType(ParseMemoryType(string(*entry.Frontmatter.Type)))
	}
	entry.Content = strings.TrimSpace(entry.Content)
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

// parseStringList 解析stringlist。
func parseStringList(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	var decoded []string
	if err := json.Unmarshal([]byte(raw), &decoded); err == nil {
		return normalizeStringSlice(decoded)
	}
	if strings.HasPrefix(raw, "[") && strings.HasSuffix(raw, "]") {
		raw = strings.TrimSuffix(strings.TrimPrefix(raw, "["), "]")
	}
	parts := strings.Split(raw, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := parseScalar(part); value != "" {
			values = append(values, value)
		}
	}
	return normalizeStringSlice(values)
}

func fallbackEntryName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.Join(strings.Fields(base), " ")
}
