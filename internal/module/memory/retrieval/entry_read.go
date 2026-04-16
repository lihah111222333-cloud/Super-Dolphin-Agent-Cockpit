package retrieval

import (
	"bufio"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const manifestHeaderScanLimit = 32 * 1024

type markdownFenceState struct {
	open   bool
	marker byte
}

type htmlCommentStripState struct {
	builder        strings.Builder
	pendingComment strings.Builder
	fence          markdownFenceState
	inComment      bool
}

func readMemoryEntryHeader(path string) (MemoryEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	defer file.Close()

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

func readMemoryEntryFile(path string) (MemoryEntry, error) {
	rawContent, err := os.ReadFile(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return MemoryEntry{}, err
	}
	content := stripUTF8BOM(string(rawContent))
	entry := MemoryEntry{Content: content, FilePath: path, UpdatedAt: info.ModTime()}
	if frontmatter, body, ok := splitMemoryFrontmatter(content); ok {
		entry.Frontmatter = parseMemoryFrontmatter(frontmatter)
		entry.Content = body
	}
	entry.Content = stripHTMLComments(entry.Content)
	entry = normalizeLoadedEntry(entry)
	if entry.Frontmatter.Name == "" {
		entry.Frontmatter.Name = fallbackEntryName(path)
	}
	entry.CanonicalName = CanonicalName(entry.Frontmatter.Name)
	return entry, nil
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

func stripUTF8BOM(content string) string { return strings.TrimPrefix(content, "\uFEFF") }

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

func parseMemoryHeader(path, header string) MemoryEntry {
	frontmatter, _, ok := splitMemoryFrontmatter(stripUTF8BOM(header))
	if !ok {
		return MemoryEntry{FilePath: path}
	}
	return MemoryEntry{Frontmatter: parseMemoryFrontmatter(frontmatter), FilePath: path}
}

func parseMemoryFrontmatter(frontmatter string) MemoryFrontmatter {
	parsed := MemoryFrontmatter{}
	for _, line := range strings.Split(frontmatter, "\n") {
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
			t := ParseMemoryType(parseScalar(value))
			parsed.Type = cloneMemoryType(t)
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

func fallbackEntryName(path string) string {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	return strings.Join(strings.Fields(base), " ")
}

func stripHTMLComments(content string) string {
	if !strings.Contains(content, "<!--") {
		return content
	}
	state := &htmlCommentStripState{}
	for _, line := range strings.SplitAfter(content, "\n") {
		state.processLine(line)
	}
	if state.inComment {
		state.builder.WriteString(state.pendingComment.String())
	}
	return state.builder.String()
}

func (s *htmlCommentStripState) processLine(line string) {
	if s.processPendingLine(line) {
		return
	}
	if lineInMarkdownFence(&s.fence, line) {
		s.builder.WriteString(line)
		return
	}
	if !startsHTMLCommentBlock(line) {
		s.builder.WriteString(line)
		return
	}
	if s.stripInlineComment(line) {
		return
	}
	s.pendingComment.WriteString(line)
	s.inComment = true
}

func (s *htmlCommentStripState) processPendingLine(line string) bool {
	if !s.inComment {
		return false
	}
	s.pendingComment.WriteString(line)
	_, residue, ok := strings.Cut(line, "-->")
	if !ok {
		return true
	}
	appendNonEmptyLine(&s.builder, residue)
	s.pendingComment.Reset()
	s.inComment = false
	return true
}

func (s *htmlCommentStripState) stripInlineComment(line string) bool {
	start := strings.Index(line, "<!--")
	end := strings.Index(line, "-->")
	if start < 0 || end < start {
		return false
	}
	appendNonEmptyLine(&s.builder, line[:start]+line[end+3:])
	return true
}

func startsHTMLCommentBlock(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "<!--")
}

func appendNonEmptyLine(builder *strings.Builder, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	builder.WriteString(line)
}

func lineInMarkdownFence(state *markdownFenceState, line string) bool {
	marker, ok := markdownFenceMarker(line)
	if state.open {
		if ok && marker == state.marker {
			state.open = false
			state.marker = 0
		}
		return true
	}
	if !ok {
		return false
	}
	state.open = true
	state.marker = marker
	return true
}

func markdownFenceMarker(line string) (byte, bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if strings.HasPrefix(trimmed, "```") {
		return '`', true
	}
	if strings.HasPrefix(trimmed, "~~~") {
		return '~', true
	}
	return 0, false
}
