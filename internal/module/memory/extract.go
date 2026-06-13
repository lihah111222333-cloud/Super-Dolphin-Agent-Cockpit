package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"

	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
)

const (
	defaultExtractMaxItems    = 8
	extractScopePrivate       = "private"
	extractScopeTeam          = "team"
	extractedMemoryNameMaxLen = 96
)

var includePattern = regexp.MustCompile(`(?:^|\s)@((?:[^\s\\]|\\ )+)`)

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

// NewMemoryExtractor 创建记忆extractor。
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

// normalizeExtractedMemories 规范化extractedmemories。
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

func extractLimit(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	if fallback > 0 {
		return fallback
	}
	return defaultExtractMaxItems
}

// ParseMemoryFile 解析记忆文件。
func ParseMemoryFile(path string) (*ParsedMemory, error) {
	rawContent, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed := parseMemoryFileContent(path, string(rawContent))
	return &parsed, nil
}

// ExtractIncludes 提取includes。
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
	content := parse.StripUTF8BOM(rawContent)
	parsed := ParsedMemory{Content: content}
	if frontmatter, body, ok := parse.SplitFrontmatter(content); ok {
		parsed.Frontmatter = parseMemoryFrontmatter(frontmatter)
		parsed.Content = body
	}
	parsed.Content = parse.StripHTMLComments(parsed.Content)
	parsed.Includes = ExtractIncludes(parsed.Content)
	parsed.Content = truncateParsedMemoryContent(path, parsed.Content)
	if parsed.Content != rawContent {
		parsed.RawContent = rawContent
		parsed.ContentDiffersFromDisk = true
	}
	return parsed
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

// isAlphaNumericOrAllowedIncludeLead 判断alphanumericallowedincludelead是否可用。
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

// ---------------------------------------------------------------------------
// ExtractionState — turn-level extraction state machine (was extract_state.go)
// ---------------------------------------------------------------------------

type ExtractionState struct {
	cursor         int64
	inProgress     bool
	pendingLatest  bool
	pendingHandled bool
	lastError      string
	mu             sync.Mutex
}

type toolCallScope struct {
	threadID string
	turnID   string
}

func turnTrackingKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

func turnWriteFiles(files map[string]struct{}) []string {
	if len(files) == 0 {
		return nil
	}
	out := make([]string, 0, len(files))
	for file := range files {
		out = append(out, file)
	}
	return uniqueNonEmptyStrings(out)
}

// extractDiffFiles 提取diff文件。
func extractDiffFiles(diffText string) []string {
	lines := strings.Split(strings.ReplaceAll(diffText, "\r\n", "\n"), "\n")
	files := make([]string, 0, len(lines))
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			files = append(files, strings.TrimSpace(strings.TrimPrefix(line, "+++ b/")))
		case strings.HasPrefix(line, "diff --git "):
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				files = append(files, strings.TrimPrefix(parts[3], "b/"))
			}
		}
	}
	return uniqueNonEmptyStrings(files)
}

func (s *ExtractionState) markPending(handled bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pendingLatest = true
	s.pendingHandled = s.pendingHandled || handled
	if s.inProgress {
		return false
	}
	s.inProgress = true
	s.lastError = ""
	return true
}

func (s *ExtractionState) beginCycle() (int64, bool, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.pendingLatest {
		return s.cursor, false, false
	}
	cursor := s.cursor
	handled := s.pendingHandled
	s.pendingLatest = false
	s.pendingHandled = false
	return cursor, handled, true
}

func (s *ExtractionState) commit(cursor int64) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cursor = cursor
	s.lastError = ""
	if !s.pendingLatest {
		s.inProgress = false
		return false
	}
	return true
}

func (s *ExtractionState) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = strings.TrimSpace(err.Error())
	s.inProgress = false
}

func (s *ExtractionState) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inProgress = false
}

func (h *MemoryLifecycleHooks) debugExtractionState(threadID string) string {
	state := h.extractionState(threadID)
	state.mu.Lock()
	defer state.mu.Unlock()
	return fmt.Sprintf("cursor=%d in_progress=%t pending=%t handled=%t error=%q",
		state.cursor,
		state.inProgress,
		state.pendingLatest,
		state.pendingHandled,
		state.lastError,
	)
}
