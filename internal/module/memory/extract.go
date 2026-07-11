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

	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"

	parse "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/parse"
)

const (
	defaultExtractMaxItems    = 8
	extractScopePrivate       = "private"
	extractScopeTeam          = "team"
	extractedMemoryNameMaxLen = 96
)

var includePattern = regexp.MustCompile(`(?:^|\s)@((?:[^\s\\]|\\ )+)`)

// ExtractFunc 是记忆抽取模型的执行入口。
// 调用方只传入整理 prompt，具体 provider 和运行策略由 fx 装配层注入。
type ExtractFunc func(ctx context.Context, prompt string) (string, error)

// ExtractParams 描述一次 transcript 记忆抽取请求。
// Manifest 提供当前磁盘记忆索引，MaxItems 限制本轮可持久化的新条目数。
type ExtractParams struct {
	Transcript []providerdto.Message
	Manifest   []MemoryEntry
	MaxItems   int
}

// ExtractedMemory 是模型返回、准备写入磁盘的结构化记忆。
// Scope 决定 private/team 目标，Type 和 Tags 会进入 frontmatter 供后续检索。
type ExtractedMemory struct {
	Scope       string
	Name        string
	Description string
	Type        MemoryType
	Content     string
	Tags        []string
}

// MemoryExtractor 保存抽取器的运行上限。
// 它只负责解析和规整模型输出，不直接读写 memory 根目录。
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

// NewMemoryExtractor 创建默认记忆抽取器。
// 默认限制用于约束单轮自动抽取数量，避免一次 transcript 生成过多持久化记忆。
func NewMemoryExtractor() *MemoryExtractor {
	return &MemoryExtractor{MaxItems: defaultExtractMaxItems}
}

// limit 返回本抽取器的条目上限。
// 未配置或非法上限会回到默认值，避免调用方传入 0 导致完全关闭抽取。
func (e *MemoryExtractor) limit() int {
	if e == nil || e.MaxItems <= 0 {
		return defaultExtractMaxItems
	}
	return e.MaxItems
}

// parseExtractedMemories 解析抽取模型返回的 JSON，并按上限规整结果。
// 生产路径只接受 {"memories":[...]} envelope；空响应和旧数组/单对象格式都会 fail-fast。
func parseExtractedMemories(raw string, limit int) ([]ExtractedMemory, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, fmt.Errorf("invalid extractor response: empty output")
	}
	items, err := decodeExtractedMemories(raw)
	if err != nil {
		return nil, err
	}
	return normalizeStrictExtractedMemories(items, limit)
}

// decodeExtractedMemories 解码严格 envelope，拒绝 legacy 数组或单对象输出。
// 这样 dream/turn 抽取不会把模型的空白或非契约输出当作成功。
func decodeExtractedMemories(raw string) ([]ExtractedMemory, error) {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("invalid extractor response: %w", err)
	}
	memoriesRaw, ok := envelope["memories"]
	if !ok {
		return nil, fmt.Errorf("invalid extractor response: memories envelope is required")
	}
	if strings.TrimSpace(string(memoriesRaw)) == "null" {
		return nil, fmt.Errorf("invalid extractor response: memories must be an array")
	}
	var envelopePayload extractEnvelope
	if err := json.Unmarshal([]byte(raw), &envelopePayload); err != nil {
		return nil, fmt.Errorf("invalid extractor response: %w", err)
	}
	return envelopePayload.Memories, nil
}

// normalizeStrictExtractedMemories 校验模型 envelope 内的每条 memory 是否满足写入契约。
// 任一条缺少 scope/name/description/type/content 或 type/scope 非法都会立即返回错误，不能靠 normalize 推断补齐。
func normalizeStrictExtractedMemories(items []ExtractedMemory, limit int) ([]ExtractedMemory, error) {
	if len(items) == 0 {
		return nil, nil
	}
	checked := make([]ExtractedMemory, 0, len(items))
	for i, item := range items {
		normalized, err := normalizeStrictExtractedMemory(item, i)
		if err != nil {
			return nil, err
		}
		checked = append(checked, normalized)
	}
	if limit <= 0 {
		return nil, nil
	}
	return normalizeExtractedMemories(checked, limit), nil
}

// normalizeStrictExtractedMemory 只接受模型明确给出的契约字段。
// 内容结构化、tag 去重等无损规整仍复用普通 normalize，但不会再为缺字段推断默认值。
func normalizeStrictExtractedMemory(item ExtractedMemory, index int) (ExtractedMemory, error) {
	scope := normalizeExtractedMemoryScope(item.Scope)
	if scope == "" {
		return ExtractedMemory{}, fmt.Errorf("invalid extractor response: memories[%d].scope must be private or team", index)
	}
	name, err := requireExtractedMemoryField(index, "name", item.Name)
	if err != nil {
		return ExtractedMemory{}, err
	}
	description, err := requireExtractedMemoryField(index, "description", item.Description)
	if err != nil {
		return ExtractedMemory{}, err
	}
	content, err := requireExtractedMemoryField(index, "content", item.Content)
	if err != nil {
		return ExtractedMemory{}, err
	}
	memoryType := ParseMemoryType(string(item.Type))
	if !memoryType.IsKnown() {
		return ExtractedMemory{}, fmt.Errorf("invalid extractor response: memories[%d].type must be user, feedback, project, or reference", index)
	}
	item.Scope = scope
	item.Name = name
	item.Description = description
	item.Content = content
	item.Type = memoryType
	return normalizeExtractedMemory(item), nil
}

// requireExtractedMemoryField 校验必填字符串字段，并返回去掉外层空白后的值。
func requireExtractedMemoryField(index int, field, value string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", fmt.Errorf("invalid extractor response: memories[%d].%s is required", index, field)
	}
	return trimmed, nil
}

// normalizeExtractedMemories 清洗、去重并裁剪抽取结果。
// scope 无效、没有正文、无法生成去重 key 或超过 limit 的条目会被丢弃，避免低质量内容进入磁盘。
func normalizeExtractedMemories(items []ExtractedMemory, limit int) []ExtractedMemory {
	if len(items) == 0 || limit <= 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	normalized := make([]ExtractedMemory, 0, minInt(len(items), limit))
	for _, item := range items {
		item = normalizeExtractedMemory(item)
		if item.Scope == "" {
			continue
		}
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

// normalizeExtractedMemory 规整单条抽取结果的 scope、类型、正文、名称和标签。
// 类型缺失时会根据内容推断；标签数量有限制，防止 frontmatter 过大。
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

// normalizeExtractedMemoryScope 将抽取 scope 限定为 private/team。
// 未知或空 scope 返回空值，由严格 parse 路径报错，内部启发式路径只会传入已知 scope。
func normalizeExtractedMemoryScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case extractScopePrivate:
		return extractScopePrivate
	case extractScopeTeam:
		return extractScopeTeam
	default:
		return ""
	}
}

// normalizeExtractedMemoryContent 选择抽取条目的持久化正文。
// 对 feedback/project 会补齐 Why/How to apply 结构，保证后续读取时有使用边界。
func normalizeExtractedMemoryContent(item ExtractedMemory) string {
	content := normalizeHookContent(firstNonEmptyExtractedPart(item.Content, item.Description, item.Name))
	if content == "" {
		return ""
	}
	return ensureStructuredExtractedContent(item.Type, content)
}

// normalizeExtractedMemoryDescription 生成索引 hook 使用的短描述。
// 描述缺失时取正文首行，并按索引 hook 预算截断。
func normalizeExtractedMemoryDescription(item ExtractedMemory) string {
	description := normalizeHookContent(item.Description)
	if description == "" {
		description = firstNonEmptyLine(item.Content)
	}
	description = strings.Join(strings.Fields(description), " ")
	return truncateRunes(description, memoryHookMaxRunes)
}

// normalizeExtractedMemoryName 生成可写入 frontmatter 的稳定名称。
// 名称缺失时依次退回描述、类型默认名，最终按文件名预算截断。
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

// ensureStructuredExtractedContent 为需要决策边界的记忆类型补齐结构化段落。
// user/reference 等类型保持原文，feedback/project 需要 Why 和 How to apply。
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

// appendMissingStructuredSections 在正文缺少关键标签时追加默认说明。
// 只追加缺失段落，不覆盖模型已经给出的更具体说明。
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

// hasStructuredExtractedLabel 判断正文是否已有指定结构化标签。
// 兼容 markdown 加粗和列表前缀，避免重复追加默认段落。
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

// defaultStructuredWhy 返回不同记忆类型的默认 Why 说明。
// 默认文案强调这是长期有效指导而非当前任务状态。
func defaultStructuredWhy(memoryType MemoryType) string {
	switch memoryType {
	case MemoryTypeProject:
		return "Why: This preserves non-obvious project context that may not be derivable from the current code or git history."
	default:
		return "Why: This is confirmed working guidance that should shape similar future work."
	}
}

// defaultStructuredHowToApply 返回不同记忆类型的默认应用边界。
// project 记忆要求先复核当前代码和文档，feedback 记忆要求可被新证据覆盖。
func defaultStructuredHowToApply(memoryType MemoryType) string {
	switch memoryType {
	case MemoryTypeProject:
		return "How to apply: Re-check the current code, docs, and user input before acting, then use this context to guide follow-up work."
	default:
		return "How to apply: Apply this rule in similar work unless newer evidence or direct user input overrides it."
	}
}

// extractedMemoryDedupKey 为抽取结果生成去重 key。
// 名称、描述、首行和全文依次兜底，确保同一事实不会在一轮内重复保存。
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

// firstNonEmptyExtractedPart 返回候选字符串中的第一个非空值。
// 抽取结果字段缺失时用于按优先级兜底。
func firstNonEmptyExtractedPart(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// nonEmptyExtractedParts 收集非空抽取字段。
// 类型推断使用这些字段组合判断记忆类别。
func nonEmptyExtractedParts(values ...string) []string {
	parts := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			parts = append(parts, trimmed)
		}
	}
	return parts
}

// extractLimit 解析抽取数量上限。
// 显式 limit 优先，其次 fallback，最后使用模块默认值。
func extractLimit(limit, fallback int) int {
	if limit > 0 {
		return limit
	}
	if fallback > 0 {
		return fallback
	}
	return defaultExtractMaxItems
}

// ParseMemoryFile 读取并解析单个记忆 markdown 文件。
// 解析阶段会清理 BOM、frontmatter、HTML 注释和 include 标记，并记录正文是否不同于磁盘。
func ParseMemoryFile(path string) (*ParsedMemory, error) {
	rawContent, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	parsed := parseMemoryFileContent(path, string(rawContent))
	return &parsed, nil
}

// ExtractIncludes 从记忆正文中提取 @path include 引用。
// 代码围栏内的 @ 引用会被忽略，结果去重后用于后续安全读取。
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

// parseMemoryFileContent 解析记忆文件内容并生成 ParsedMemory。
// frontmatter 缺失时保留正文；索引文件和正文文件走不同截断规则。
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

// truncateParsedMemoryContent 对 MEMORY.md 入口文件应用入口截断规则。
// 普通记忆正文不在解析阶段截断，避免读取单条记忆时丢内容。
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

// normalizeIncludePath 清理 @include 提取出的路径文本。
// escaped space 会还原，fragment 会被剥离，后续再做路径形态校验。
func normalizeIncludePath(path string) string {
	path = strings.ReplaceAll(path, `\ `, " ")
	path, _, _ = strings.Cut(path, "#")
	return strings.TrimSpace(path)
}

// isValidIncludePath 判断 include 路径是否具备可解析的基本形态。
// 这里只做语法级过滤，真正读文件时仍必须走路径安全校验。
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

// isAlphaNumericOrAllowedIncludeLead 判断 include 相对路径首字符是否合法。
// 允许字母数字和常见文件名前缀，拒绝明显不是路径的标点文本。
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

// lineInMarkdownFence 更新代码围栏状态并返回当前行是否在围栏内。
// include 提取会跳过围栏内容，避免示例代码里的 @path 被当成真实引用。
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

// markdownFenceMarker 识别 Markdown 代码围栏标记。
// 支持反引号和波浪线两类围栏，语言后缀不影响判断。
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

// ExtractionState 是单个 thread 的后台抽取状态机。
// cursor 记录已处理 transcript 位置，pendingLatest/pendingHandled 合并并发入队请求；
// mu 保护所有字段，调用方不得无锁读取。
type ExtractionState struct {
	cursor         int64
	inProgress     bool
	pendingLatest  bool
	pendingHandled bool
	lastError      string
	mu             sync.Mutex
}

// toolCallScope 保存工具调用所属的 thread/turn。
// ToolDiffUpdated 缺少 turnID 时会用它恢复归属。
type toolCallScope struct {
	threadID string
	turnID   string
}

// turnTrackingKey 生成 thread/turn 追踪表 key。
// 使用 NUL 分隔可避免简单字符串拼接产生歧义。
func turnTrackingKey(threadID, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}

// turnWriteFiles 将本轮工具写入集合转为去重文件列表。
// 空集合返回 nil，便于上游直接判断无工具写入。
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

// extractDiffFiles 从统一 diff 文本中提取被写入的文件路径。
// 该兜底用于 ToolDiffUpdated 未显式携带文件列表的 provider。
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

// markPending 标记 thread 需要执行一轮后台抽取。
// 返回 true 表示调用方需要启动 goroutine；已有 goroutine 时只合并 pending 状态。
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

// beginCycle 取出下一轮抽取游标和 handled 标记。
// 没有 pending 时返回 ok=false，worker 可结束或 finish。
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

// commit 提交本轮抽取的新游标。
// 如果提交期间没有新的 pending，则清除 inProgress 并返回 false；否则 worker 继续下一轮。
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

// fail 记录后台抽取错误并释放 inProgress。
// 后续新 turn 可再次 markPending，错误只用于调试状态展示。
func (s *ExtractionState) fail(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastError = strings.TrimSpace(err.Error())
	s.inProgress = false
}

// finish 在未执行抽取或关闭回滚时释放 inProgress。
// 它不修改 cursor，避免未处理 transcript 被错误跳过。
func (s *ExtractionState) finish() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.inProgress = false
}

// debugExtractionState 返回 thread 抽取状态的调试字符串。
// 仅供测试和诊断使用，读取时持有状态锁以保证字段一致。
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
