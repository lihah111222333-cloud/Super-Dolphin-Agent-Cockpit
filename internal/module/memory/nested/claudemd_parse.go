package nested

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"

	"golang.org/x/text/unicode/norm"
)

func parseClaudeRuleContent(content string) (claudeRuleMetadata, string) {
	frontmatter, body, ok := splitMemoryFrontmatter(stripUTF8BOM(content))
	if !ok {
		return claudeRuleMetadata{}, strings.TrimSpace(StripHTMLComments(content))
	}
	metadata := parseClaudeRuleMetadata(frontmatter)
	return metadata, strings.TrimSpace(StripHTMLComments(body))
}

func parseBoolEnv(key string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func normalizeStringSlice(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(values))
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.Join(strings.Fields(norm.NFC.String(strings.TrimSpace(value))), " ")
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, value)
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
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

func stripUTF8BOM(content string) string {
	return strings.TrimPrefix(content, "\uFEFF")
}

var htmlCommentLinePattern = regexp.MustCompile(`<!--.*?-->`)

func StripHTMLComments(content string) string {
	stripped, _ := stripHTMLComments(content)
	return stripped
}

func stripHTMLComments(content string) (string, bool) {
	if !strings.Contains(content, "<!--") {
		return content, false
	}
	state := &htmlCommentStripState{}
	for _, line := range strings.SplitAfter(content, "\n") {
		state.processLine(line)
	}
	if state.inComment {
		state.builder.WriteString(state.pendingComment.String())
	}
	return state.builder.String(), state.stripped
}

type markdownFenceState struct {
	open   bool
	marker byte
}

type htmlCommentStripState struct {
	fence          markdownFenceState
	builder        strings.Builder
	pendingComment strings.Builder
	stripped       bool
	inComment      bool
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
	s.stripped = true
	appendNonEmptyLine(&s.builder, residue)
	s.pendingComment.Reset()
	s.inComment = false
	return true
}

func (s *htmlCommentStripState) stripInlineComment(line string) bool {
	if !strings.Contains(line, "-->") {
		return false
	}
	s.stripped = true
	appendNonEmptyLine(&s.builder, htmlCommentLinePattern.ReplaceAllString(line, ""))
	return true
}

func appendNonEmptyLine(builder *strings.Builder, line string) {
	if strings.TrimSpace(line) == "" {
		return
	}
	builder.WriteString(line)
}

func startsHTMLCommentBlock(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "<!--")
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
