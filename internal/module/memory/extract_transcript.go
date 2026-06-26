package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func normalizeTranscriptMessages(messages []providerdto.Message) []providerdto.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]providerdto.Message, 0, len(messages))
	for idx, msg := range messages {
		msg.ID = normalizedTranscriptID(msg.ID, idx)
		msg.Role = normalizedTranscriptRole(msg)
		msg.Content = strings.TrimSpace(msg.Content)
		if msg.Content == "" {
			continue
		}
		out = append(out, msg)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func normalizedTranscriptID(id int64, idx int) int64 {
	if id > 0 {
		return id
	}
	return int64(idx + 1)
}

func normalizedTranscriptRole(msg providerdto.Message) string {
	role := strings.ToLower(strings.TrimSpace(msg.Role))
	if role != "" {
		return role
	}
	if strings.EqualFold(strings.TrimSpace(msg.EventType), "agent_message") {
		return "assistant"
	}
	return "user"
}

func latestTranscriptCursor(messages []providerdto.Message) int64 {
	if len(messages) == 0 {
		return 0
	}
	return messages[len(messages)-1].ID
}

func transcriptWindow(messages []providerdto.Message, cursor int64) []providerdto.Message {
	window := make([]providerdto.Message, 0, len(messages))
	for _, msg := range normalizeTranscriptMessages(messages) {
		if msg.ID <= cursor {
			continue
		}
		window = append(window, msg)
	}
	return window
}

// Extract 提取记忆。
func (e *MemoryExtractor) Extract(ctx context.Context, fn ExtractFunc, params ExtractParams) ([]ExtractedMemory, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	transcript := normalizeTranscriptMessages(params.Transcript)
	if len(transcript) == 0 {
		return nil, nil
	}
	limit := extractLimit(params.MaxItems, e.limit())
	if fn == nil {
		return filterManifestDuplicates(extractInternalMemories(transcript, params.Manifest, limit), params.Manifest), nil
	}
	raw, err := fn(ctx, buildExtractPrompt(ExtractParams{
		Transcript: transcript,
		Manifest:   params.Manifest,
		MaxItems:   limit,
	}, e.limit()))
	if err != nil {
		return nil, err
	}
	items, err := parseExtractedMemories(raw, limit)
	if err != nil {
		return nil, err
	}
	return filterManifestDuplicates(items, params.Manifest), nil
}

func buildExtractPrompt(params ExtractParams, fallbackLimit int) string {
	limit := extractLimit(params.MaxItems, fallbackLimit)
	parts := []string{
		"Distill only durable memory worth carrying into future sessions.",
		"Prefer novel memories not already present in the existing manifest.",
		"Every returned memory MUST include a `type` selected from the four-type taxonomy below. If you cannot justify a taxonomy type, omit the memory instead of returning an untyped item.",
		renderExtractTaxonomy(),
		renderExtractExclusions(),
		"Return JSON in the form {\"memories\": [{\"scope\":\"private|team\",\"name\":\"...\",\"description\":\"...\",\"type\":\"user|feedback|project|reference\",\"content\":\"...\"}]} with one object per durable memory.",
		"Every memory item must include `scope`, `name`, `description`, `type`, and `content`.",
		"For `feedback` and `project` memories, `content` must be structured as the main rule/fact followed by `Why:` and `How to apply:` lines.",
		fmt.Sprintf("Limit the response to %d memory items.", limit),
		"Existing memory manifest (header-only):",
		renderManifestHeaders(params.Manifest),
		"Conversation transcript:",
		renderTranscriptMessages(params.Transcript),
	}
	return strings.Join(parts, "\n\n")
}

func renderExtractTaxonomy() string {
	parts := []string{
		"## Four memory types",
		"Use only `user`, `feedback`, `project`, or `reference`.",
		"Choose `type` by semantic meaning of the content. Storage path, scope, and mode are separate concerns.",
	}
	for _, memoryType := range diskMemoryTypes {
		behavior := standardMemoryTypeBehaviors[memoryType]
		section := []string{
			"### " + string(memoryType),
			renderBullets(append([]string{behavior.Summary}, behavior.Save...)),
		}
		parts = append(parts, strings.Join(nonEmpty(section), "\n"))
	}
	return strings.Join(parts, "\n\n")
}

func renderExtractExclusions() string {
	return strings.Join(nonEmpty([]string{
		"## What not to save",
		renderBullets(standardExclusionRules),
	}), "\n")
}

func renderManifestHeaders(manifest []MemoryEntry) string {
	if len(manifest) == 0 {
		return "(empty)"
	}
	limit := minInt(len(manifest), 40)
	lines := make([]string, 0, limit)
	for _, entry := range manifest[:limit] {
		lines = append(lines, fmt.Sprintf(
			"- %s | type=%s | name=%s | description=%s",
			relativeMemoryPath("", entry.FilePath),
			entry.Type(),
			strings.TrimSpace(entry.Frontmatter.Name),
			strings.TrimSpace(entry.Frontmatter.Description),
		))
	}
	return strings.Join(lines, "\n")
}

func renderTranscriptMessages(messages []providerdto.Message) string {
	transcript := normalizeTranscriptMessages(messages)
	lines := make([]string, 0, len(transcript))
	for _, msg := range transcript {
		lines = append(lines, fmt.Sprintf("[%d] %s: %s", msg.ID, msg.Role, msg.Content))
	}
	return strings.Join(lines, "\n")
}

// filterManifestDuplicates 过滤已经存在于 manifest 中的抽取候选。
// 同时按 canonical name 和描述建立 seen 集合，避免把已有记忆再次写入磁盘。
func filterManifestDuplicates(items []ExtractedMemory, manifest []MemoryEntry) []ExtractedMemory {
	if len(items) == 0 || len(manifest) == 0 {
		return items
	}
	seen := make(map[string]struct{}, len(manifest)*2)
	for _, entry := range manifest {
		addManifestKey(seen, entry.CanonicalName)
		addManifestKey(seen, CanonicalName(entry.Frontmatter.Description))
	}
	filtered := make([]ExtractedMemory, 0, len(items))
	for _, item := range items {
		key := extractedMemoryDedupKey(item)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, item)
	}
	return filtered
}

// addManifestKey 向去重集合加入非空 canonical key。
// 空 key 会跳过，避免把所有无名条目误判为同一项。
func addManifestKey(seen map[string]struct{}, key string) {
	if key = strings.TrimSpace(key); key != "" {
		seen[key] = struct{}{}
	}
}

// extractInternalMemories 使用启发式规则从 transcript 中抽取候选记忆。
// 结果仍会经过 manifest 去重和 normalizeExtractedMemories 限制，防止启发式过度写入。
func extractInternalMemories(messages []providerdto.Message, manifest []MemoryEntry, limit int) []ExtractedMemory {
	candidates := make([]ExtractedMemory, 0, minInt(len(messages), limit))
	for _, msg := range messages {
		item, ok := internalMemoryFromMessage(msg)
		if !ok {
			continue
		}
		candidates = append(candidates, item)
	}
	return normalizeExtractedMemories(filterManifestDuplicates(candidates, manifest), limit)
}

// internalMemoryFromMessage 将单条消息转换为可能的记忆候选。
// 代码块内容直接跳过；角色和 cue 决定记忆类型，避免普通对话被无差别保存。
func internalMemoryFromMessage(msg providerdto.Message) (ExtractedMemory, bool) {
	text := condensedMemoryText(msg.Content)
	if text == "" || strings.Contains(text, "```") {
		return ExtractedMemory{}, false
	}
	lower := strings.ToLower(text)
	switch {
	case strings.EqualFold(msg.Role, "user") && containsHeuristicCue(lower, userPreferenceCues...):
		return ExtractedMemory{Scope: extractScopePrivate, Content: text, Type: MemoryTypeUser}, true
	case strings.EqualFold(msg.Role, "assistant") && containsHeuristicCue(lower, assistantPreferenceCues...):
		return ExtractedMemory{Scope: extractScopePrivate, Content: text, Type: MemoryTypeFeedback}, true
	case containsHeuristicCue(lower, referenceCues...) && strings.Contains(text, "http"):
		return ExtractedMemory{Scope: extractScopePrivate, Content: text, Type: MemoryTypeReference}, true
	case containsHeuristicCue(lower, projectCues...):
		return ExtractedMemory{Scope: extractScopePrivate, Content: text, Type: MemoryTypeProject}, true
	default:
		return ExtractedMemory{}, false
	}
}

func condensedMemoryText(text string) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if len([]rune(text)) < 12 {
		return ""
	}
	return truncateRunes(text, 280)
}

func containsHeuristicCue(text string, cues ...string) bool {
	for _, cue := range cues {
		if strings.Contains(text, cue) {
			return true
		}
	}
	return false
}

var userPreferenceCues = []string{
	"prefer", "preference", "i like", "keep diffs", "please keep", "以后请", "偏好", "喜欢", "记住",
}

var assistantPreferenceCues = []string{
	"you prefer", "keep diffs", "偏好", "喜欢", "记住了",
}

var projectCues = []string{
	"repo", "project", "build", "test", "deploy", "service", "branch", "ticket", "仓库", "项目", "构建", "测试", "部署",
}

var referenceCues = []string{
	"http://", "https://", "dashboard", "doc", "wiki", "grafana", "链接", "地址",
}
