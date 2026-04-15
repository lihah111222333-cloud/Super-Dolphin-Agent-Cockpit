package memory

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const (
	memoryHookMaxRunes      = 150
	manifestHeaderScanLimit = 32 * 1024
)

type MemoryIndexEntry struct {
	Title         string
	Path          string
	Hook          string
	CanonicalName string
}

func ParseMemoryIndex(content string) ([]MemoryIndexEntry, error) {
	content = stripUTF8BOM(content)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	entries := make([]MemoryIndexEntry, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		entry, err := parseIndexLine(line)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func stripUTF8BOM(content string) string {
	return strings.TrimPrefix(content, "\uFEFF")
}

func ReadMemoryIndex(path string) ([]MemoryIndexEntry, error) {
	validatedPath, err := ValidateMemoryReadPath(filepath.Dir(path), path)
	if err != nil {
		return nil, err
	}
	content, err := os.ReadFile(validatedPath)
	if err != nil {
		return nil, err
	}
	return ParseMemoryIndex(string(content))
}

func readMemoryEntryHeader(path string) (MemoryEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	defer func() { _ = file.Close() }()

	header, err := readFrontmatterHeader(file)
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

func readFrontmatterHeader(r io.Reader) (string, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, manifestHeaderScanLimit))
	scanner.Buffer(make([]byte, 0, 4096), manifestHeaderScanLimit)
	var builder strings.Builder
	openMarkers := 0
	for scanner.Scan() {
		line := scanner.Text()
		builder.WriteString(line)
		builder.WriteByte('\n')
		if strings.TrimSpace(line) == "---" {
			openMarkers++
			if openMarkers >= 2 {
				break
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

func parseMemoryHeader(path, header string) MemoryEntry {
	content := stripUTF8BOM(header)
	frontmatter, _, ok := splitMemoryFrontmatter(content)
	if !ok {
		return MemoryEntry{FilePath: path}
	}
	return MemoryEntry{
		Frontmatter: parseMemoryFrontmatter(frontmatter),
		FilePath:    path,
	}
}

func WriteMemoryIndex(root string, entries []MemoryEntry) error {
	indexEntries, err := buildMemoryIndex(root, entries)
	if err != nil {
		return err
	}
	return writeAtomicFile(memoryIndexPath(root), []byte(formatMemoryIndex(indexEntries)), 0o644)
}

func UpdateMemoryIndex(root string) ([]MemoryIndexEntry, error) {
	entries, err := scanMemoryEntries(root)
	if err != nil {
		return nil, err
	}
	if err := WriteMemoryIndex(root, entries); err != nil {
		return nil, err
	}
	return buildMemoryIndex(root, entries)
}

func RebuildMemoryIndex(root string) ([]MemoryIndexEntry, error) {
	return UpdateMemoryIndex(root)
}

func scanMemoryEntries(root string) ([]MemoryEntry, error) {
	exists, err := memoryEntriesRootExists(root)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	entries := make([]MemoryEntry, 0, 16)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		entry, skipDir, ok, err := scannedMemoryEntry(root, path, d, walkErr)
		if err != nil {
			return err
		}
		if skipDir {
			return filepath.SkipDir
		}
		if ok {
			entries = append(entries, entry)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortEntries(entries)
	return entries, nil
}

func memoryEntriesRootExists(root string) (bool, error) {
	if _, err := os.Stat(root); errorsIsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	return true, nil
}

func scannedMemoryEntry(root, path string, d fs.DirEntry, walkErr error) (MemoryEntry, bool, bool, error) {
	if walkErr != nil {
		return MemoryEntry{}, false, false, walkErr
	}
	if d.IsDir() {
		return MemoryEntry{}, isConsolidationLogPath(root, path), false, nil
	}
	if shouldSkipScannedMemoryPath(root, path) {
		return MemoryEntry{}, false, false, nil
	}
	if _, err := ValidateMemoryReadPath(root, path); err != nil {
		return MemoryEntry{}, false, false, err
	}
	entry, err := readMemoryEntryFile(path)
	if err != nil {
		return MemoryEntry{}, false, false, err
	}
	return entry, false, true, nil
}

func shouldSkipScannedMemoryPath(root, path string) bool {
	return filepath.Ext(path) != ".md" ||
		filepath.Base(path) == memoryIndexFileName ||
		isConsolidationLogPath(root, path)
}

func readMemoryEntryFile(path string) (MemoryEntry, error) {
	parsed, err := ParseMemoryFile(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	entry := MemoryEntry{Content: parsed.Content, FilePath: path, UpdatedAt: info.ModTime()}
	entry.Frontmatter = parsed.Frontmatter
	entry = normalizeLoadedEntry(entry)
	if entry.Frontmatter.Name == "" {
		entry.Frontmatter.Name = fallbackEntryName(path)
	}
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return entry, nil
}

func buildMemoryIndex(root string, entries []MemoryEntry) ([]MemoryIndexEntry, error) {
	uniqueEntries := uniqueEntriesByCanonicalName(entries)
	indexEntries := make([]MemoryIndexEntry, 0, len(uniqueEntries))
	for _, entry := range uniqueEntries {
		rel, err := filepath.Rel(root, entry.FilePath)
		if err != nil {
			return nil, err
		}
		indexEntries = append(indexEntries, MemoryIndexEntry{
			Title:         strings.TrimSpace(entry.Frontmatter.Name),
			Path:          filepath.ToSlash(rel),
			Hook:          hookFromEntry(entry),
			CanonicalName: entry.CanonicalName,
		})
	}
	return indexEntries, nil
}

func formatMemoryIndex(entries []MemoryIndexEntry) string {
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		line := fmt.Sprintf("- [%s](%s)", entry.Title, entry.Path)
		if hook := strings.TrimSpace(entry.Hook); hook != "" {
			line += " — " + hook
		}
		lines = append(lines, line)
	}
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

func formatMemoryEntry(entry MemoryEntry) string {
	frontmatter := entry.Frontmatter
	lines := []string{
		"---",
		"name: " + strconv.Quote(frontmatter.Name),
		"description: " + strconv.Quote(frontmatter.Description),
		"type: " + strconv.Quote(string(entry.Type())),
	}
	if frontmatter.Lang != "" {
		lines = append(lines, "lang: "+strconv.Quote(frontmatter.Lang))
	}
	if len(frontmatter.Aliases) > 0 {
		lines = append(lines, "aliases: "+formatStringList(frontmatter.Aliases))
	}
	if len(frontmatter.SearchKeys) > 0 {
		lines = append(lines, "search_keys: "+formatStringList(frontmatter.SearchKeys))
	}
	lines = append(lines, "---", "", strings.TrimSpace(entry.Content), "")
	return strings.Join(lines, "\n")
}

func parseIndexLine(line string) (MemoryIndexEntry, error) {
	if !strings.HasPrefix(line, "- [") {
		return MemoryIndexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	rest := strings.TrimPrefix(line, "- [")
	title, tail, ok := strings.Cut(rest, "](")
	if !ok {
		return MemoryIndexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	path, hook, ok := strings.Cut(tail, ")")
	if !ok {
		return MemoryIndexEntry{}, fmt.Errorf("invalid MEMORY.md line: %q", line)
	}
	entry := MemoryIndexEntry{Title: strings.TrimSpace(title), Path: strings.TrimSpace(path)}
	hook = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(hook), "—"))
	entry.Hook = hook
	entry.CanonicalName = CanonicalName(entry.Title)
	return entry, nil
}

func splitMemoryFrontmatter(content string) (string, string, bool) {
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

func parseMemoryFrontmatter(frontmatter string) MemoryFrontmatter {
	parsed := MemoryFrontmatter{}
	for line := range strings.SplitSeq(frontmatter, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "name":
			parsed.Name = parseScalar(value)
		case "description":
			parsed.Description = parseScalar(value)
		case "type":
			memoryType := ParseMemoryType(parseScalar(value))
			parsed.Type = cloneMemoryType(memoryType)
		case "lang":
			parsed.Lang = parseScalar(value)
		case "aliases":
			parsed.Aliases = parseStringList(value)
		case "search_keys":
			parsed.SearchKeys = parseStringList(value)
		}
	}
	return parsed
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

func hookFromEntry(entry MemoryEntry) string {
	hook := strings.TrimSpace(entry.Frontmatter.Description)
	if hook == "" {
		hook = firstNonEmptyLine(entry.Content)
	}
	hook = strings.Join(strings.Fields(hook), " ")
	return truncateRunes(hook, memoryHookMaxRunes)
}

func formatStringList(values []string) string {
	encoded, _ := json.Marshal(values)
	return string(encoded)
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit]))
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

func fallbackEntryName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.Join(strings.Fields(base), " ")
}

func uniqueEntriesByCanonicalName(entries []MemoryEntry) []MemoryEntry {
	if len(entries) == 0 {
		return nil
	}
	selected := make(map[string]MemoryEntry, len(entries))
	for _, entry := range entries {
		key := entry.CanonicalName
		if key == "" {
			key = CanonicalName(entry.Frontmatter.Name)
		}
		current, exists := selected[key]
		if !exists || preferMemoryEntry(entry, current) {
			selected[key] = entry
		}
	}
	uniqueEntries := make([]MemoryEntry, 0, len(selected))
	for _, entry := range selected {
		uniqueEntries = append(uniqueEntries, entry)
	}
	sortEntries(uniqueEntries)
	return uniqueEntries
}

func preferMemoryEntry(candidate, current MemoryEntry) bool {
	if candidate.UpdatedAt.Equal(current.UpdatedAt) {
		return candidate.FilePath < current.FilePath
	}
	return candidate.UpdatedAt.After(current.UpdatedAt)
}

func sortEntries(entries []MemoryEntry) {
	sort.Slice(entries, func(i, j int) bool {
		left := entries[i].CanonicalName
		right := entries[j].CanonicalName
		if left == right {
			return entries[i].FilePath < entries[j].FilePath
		}
		return left < right
	})
}

func errorsIsNotExist(err error) bool {
	return err != nil && os.IsNotExist(err)
}
