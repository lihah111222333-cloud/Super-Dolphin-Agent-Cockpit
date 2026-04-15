package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const (
	defaultExtractMaxItems    = 8
	extractScopePrivate       = "private"
	extractScopeTeam          = "team"
	extractedMemoryNameMaxLen = 96
)

var (
	htmlCommentLinePattern = regexp.MustCompile(`<!--.*?-->`)
	includePattern         = regexp.MustCompile(`(?:^|\s)@((?:[^\s\\]|\\ )+)`)
)

type ExtractFunc func(ctx context.Context, prompt string) (string, error)

type ExtractParams struct {
	Transcript []providerdto.Message
	Manifest   []MemoryEntry
	MaxItems   int
}

type ExtractedMemory struct {
	Scope       string
	Name        string
	Description string
	Type        MemoryType
	Content     string
	Tags        []string
}

type MemoryExtractor struct {
	MaxItems int
}

type extractEnvelope struct {
	Memories []ExtractedMemory `json:"memories"`
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

func NewMemoryExtractor() *MemoryExtractor {
	return &MemoryExtractor{MaxItems: defaultExtractMaxItems}
}

func (e *MemoryExtractor) limit() int {
	if e == nil || e.MaxItems <= 0 {
		return defaultExtractMaxItems
	}
	return e.MaxItems
}

func parseExtractedMemories(raw string, limit int) ([]ExtractedMemory, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	items, err := decodeExtractedMemories(raw)
	if err != nil {
		return nil, err
	}
	return normalizeExtractedMemories(items, limit), nil
}

func decodeExtractedMemories(raw string) ([]ExtractedMemory, error) {
	if strings.Contains(raw, `"memories"`) {
		var envelope extractEnvelope
		if err := json.Unmarshal([]byte(raw), &envelope); err == nil {
			return envelope.Memories, nil
		}
	}
	var list []ExtractedMemory
	if err := json.Unmarshal([]byte(raw), &list); err == nil {
		return list, nil
	}
	var single ExtractedMemory
	if err := json.Unmarshal([]byte(raw), &single); err == nil {
		return []ExtractedMemory{single}, nil
	}
	return nil, fmt.Errorf("invalid extractor response")
}

func normalizeExtractedMemories(items []ExtractedMemory, limit int) []ExtractedMemory {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	normalized := make([]ExtractedMemory, 0, minInt(len(items), limit))
	for _, item := range items {
		item = normalizeExtractedMemory(item)
		if strings.TrimSpace(item.Content) == "" {
			continue
		}
		key := extractedMemoryDedupKey(item)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, item)
		if len(normalized) >= limit {
			break
		}
	}
	return normalized
}

func normalizeExtractedMemory(item ExtractedMemory) ExtractedMemory {
	item.Scope = normalizeExtractedMemoryScope(item.Scope)
	item.Type = ParseMemoryType(string(item.Type))
	if !item.Type.IsKnown() {
		item.Type = inferMemoryType(strings.Join(nonEmptyExtractedParts(item.Name, item.Description, item.Content), "\n"))
	}
	item.Content = normalizeExtractedMemoryContent(item)
	item.Description = normalizeExtractedMemoryDescription(item)
	item.Name = normalizeExtractedMemoryName(item)
	item.Tags = normalizeStringSlice(item.Tags)
	if len(item.Tags) > 6 {
		item.Tags = item.Tags[:6]
	}
	return item
}

func normalizeExtractedMemoryScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case extractScopeTeam:
		return extractScopeTeam
	default:
		return extractScopePrivate
	}
}

func normalizeExtractedMemoryContent(item ExtractedMemory) string {
	content := normalizeHookContent(firstNonEmptyExtractedPart(item.Content, item.Description, item.Name))
	if content == "" {
		return ""
	}
	return ensureStructuredExtractedContent(item.Type, content)
}

func normalizeExtractedMemoryDescription(item ExtractedMemory) string {
	description := normalizeHookContent(item.Description)
	if description == "" {
		description = firstNonEmptyLine(item.Content)
	}
	description = strings.Join(strings.Fields(description), " ")
	return truncateRunes(description, memoryHookMaxRunes)
}

func normalizeExtractedMemoryName(item ExtractedMemory) string {
	name := normalizeHookContent(item.Name)
	if name == "" {
		name = item.Description
	}
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		if item.Type.IsKnown() {
			name = string(item.Type) + " note"
		} else {
			name = "memory note"
		}
	}
	return truncateRunes(name, extractedMemoryNameMaxLen)
}

func ensureStructuredExtractedContent(memoryType MemoryType, content string) string {
	if strings.TrimSpace(content) == "" {
		return ""
	}
	switch memoryType {
	case MemoryTypeFeedback, MemoryTypeProject:
		return appendMissingStructuredSections(memoryType, content)
	default:
		return content
	}
}

func appendMissingStructuredSections(memoryType MemoryType, content string) string {
	lines := []string{strings.TrimSpace(content)}
	if !hasStructuredExtractedLabel(content, "why") {
		lines = append(lines, defaultStructuredWhy(memoryType))
	}
	if !hasStructuredExtractedLabel(content, "how to apply") {
		lines = append(lines, defaultStructuredHowToApply(memoryType))
	}
	return strings.Join(lines, "\n")
}

func hasStructuredExtractedLabel(content, label string) bool {
	for line := range strings.SplitSeq(content, "\n") {
		normalized := strings.ToLower(strings.TrimSpace(line))
		normalized = strings.TrimPrefix(normalized, "- ")
		normalized = strings.ReplaceAll(normalized, "**", "")
		if strings.HasPrefix(normalized, label+":") {
			return true
		}
	}
	return false
}

func defaultStructuredWhy(memoryType MemoryType) string {
	switch memoryType {
	case MemoryTypeProject:
		return "Why: This preserves non-obvious project context that may not be derivable from the current code or git history."
	default:
		return "Why: This is confirmed working guidance that should shape similar future work."
	}
}

func defaultStructuredHowToApply(memoryType MemoryType) string {
	switch memoryType {
	case MemoryTypeProject:
		return "How to apply: Re-check the current code, docs, and user input before acting, then use this context to guide follow-up work."
	default:
		return "How to apply: Apply this rule in similar work unless newer evidence or direct user input overrides it."
	}
}

func extractedMemoryDedupKey(item ExtractedMemory) string {
	for _, candidate := range []string{
		CanonicalName(item.Name),
		CanonicalName(item.Description),
		CanonicalName(firstNonEmptyLine(item.Content)),
		CanonicalName(item.Content),
	} {
		if candidate != "" {
			return candidate
		}
	}
	return ""
}

func firstNonEmptyExtractedPart(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nonEmptyExtractedParts(values ...string) []string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

func extractPromptTaxonomySection() string {
	engine := resolvedRuleEngine(nil)
	lines := make([]string, 0, len(engine.order)+1)
	for _, memoryType := range engine.order {
		behavior := engine.rules[memoryType]
		line := fmt.Sprintf("`%s`: %s", memoryType, behavior.Summary)
		if memoryType == MemoryTypeFeedback || memoryType == MemoryTypeProject {
			line += " Body must be structured as the main rule/fact followed by `Why:` and `How to apply:` lines."
		}
		lines = append(lines, line)
	}
	lines = append(lines, "`scope` must be `private` or `team`; default to `private` unless the memory is safe and broadly useful to collaborators.")
	return renderSection("Memory taxonomy", lines)
}

func extractPromptExclusionsSection() string {
	return renderSection("What not to save", standardExclusionRules)
}

func extractLimit(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	if fallback > 0 {
		return fallback
	}
	return defaultExtractMaxItems
}

func ParseMemoryFile(path string) (*ParsedMemory, error) {
	rawContent, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed := parseMemoryFileContent(path, string(rawContent))
	return &parsed, nil
}

func StripHTMLComments(content string) string {
	stripped, _ := stripHTMLComments(content)
	return stripped
}

func ExtractIncludes(content string) []string {
	if !strings.Contains(content, "@") {
		return nil
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	fence := markdownFenceState{}
	seen := make(map[string]struct{}, 4)
	includes := make([]string, 0, 4)
	for _, line := range lines {
		if lineInMarkdownFence(&fence, line) {
			continue
		}
		for _, match := range includePattern.FindAllStringSubmatch(line, -1) {
			path := normalizeIncludePath(match[1])
			if !isValidIncludePath(path) {
				continue
			}
			if _, ok := seen[path]; ok {
				continue
			}
			seen[path] = struct{}{}
			includes = append(includes, path)
		}
	}
	if len(includes) == 0 {
		return nil
	}
	return includes
}

func parseMemoryFileContent(path, rawContent string) ParsedMemory {
	content := stripUTF8BOM(rawContent)
	parsed := ParsedMemory{Content: content}
	if frontmatter, body, ok := splitMemoryFrontmatter(content); ok {
		parsed.Frontmatter = parseMemoryFrontmatter(frontmatter)
		parsed.Content = body
	}
	parsed.Content = StripHTMLComments(parsed.Content)
	parsed.Includes = ExtractIncludes(parsed.Content)
	parsed.Content = truncateParsedMemoryContent(path, parsed.Content)
	if parsed.Content != rawContent {
		parsed.RawContent = rawContent
		parsed.ContentDiffersFromDisk = true
	}
	return parsed
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

func truncateParsedMemoryContent(path, content string) string {
	if !strings.EqualFold(filepath.Base(path), memoryIndexFileName) {
		return content
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	return TruncateEntrypointContent(trimmed).Content
}

func startsHTMLCommentBlock(line string) bool {
	return strings.HasPrefix(strings.TrimLeft(line, " \t"), "<!--")
}

func normalizeIncludePath(path string) string {
	path = strings.ReplaceAll(path, `\ `, " ")
	path, _, _ = strings.Cut(path, "#")
	return strings.TrimSpace(path)
}

func isValidIncludePath(path string) bool {
	if path == "" {
		return false
	}
	if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "~/") {
		return len(path) > 2
	}
	if strings.HasPrefix(path, "/") {
		return len(path) > 1
	}
	return isAlphaNumericOrAllowedIncludeLead(path[0])
}

func isAlphaNumericOrAllowedIncludeLead(first byte) bool {
	if first == '.' || first == '_' || first == '-' {
		return true
	}
	if first >= '0' && first <= '9' {
		return true
	}
	if first >= 'A' && first <= 'Z' {
		return true
	}
	return first >= 'a' && first <= 'z'
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
