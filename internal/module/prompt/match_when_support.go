package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
)

// matchWhenStringValue 从 JSON 条件值中取字符串，类型不匹配时返回空串表示条件不成立。
func matchWhenStringValue(want any) string {
	value, ok := want.(string)
	if !ok {
		return ""
	}
	return value
}

// matchCWDGlob 用 slash 归一化后的路径执行 cwd_glob 匹配。
func matchCWDGlob(pattern string, cwd string) bool {
	if pattern == "" || cwd == "" {
		return false
	}
	matched, err := filepath.Match(filepath.ToSlash(pattern), filepath.ToSlash(cwd))
	return err == nil && matched
}

// matchCWDPrefix 用 slash 归一化后的路径执行 cwd_prefix 匹配。
func matchCWDPrefix(prefix string, cwd string) bool {
	if prefix == "" || cwd == "" {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(cwd), filepath.ToSlash(prefix))
}

// matchTagsHas 在用户提示中做大小写不敏感的子串探测。
func matchTagsHas(keyword string, userPrompt string) bool {
	if keyword == "" || userPrompt == "" {
		return false
	}
	return strings.Contains(
		strings.ToLower(userPrompt),
		strings.ToLower(keyword),
	)
}

// storePromptTemplateAndContent 保存模板，并在 section 化模板上把正文写入对应内容 section。
func storePromptTemplateAndContent(
	ctx context.Context,
	store Store,
	template Template,
	current *Template,
	contentSection *TemplateSection,
	content string,
) (*Template, error) {
	if contentSection != nil && current != nil {
		template.PromptText = current.PromptText
		template.Tags = withPromptInferredIntentTag(template.Tags, promptSectionInferredIntentKind(*contentSection))
	}
	saved, err := store.Upsert(ctx, template)
	if err != nil {
		return nil, err
	}
	if contentSection == nil {
		return saved, nil
	}
	section := *contentSection
	if section.TemplateID == 0 {
		section.TemplateID = saved.ID
	}
	section.Body = content
	if _, err := store.UpsertSection(ctx, section); err != nil {
		return nil, err
	}
	return saved, nil
}

// promptContentSectionTargetForWrite 为更新请求寻找可承载 content 的 section。
// 对必须 section 化的 recall/default_rule 模板，找不到目标 section 会立即报错。
func promptContentSectionTargetForWrite(
	ctx context.Context,
	store Store,
	current *Template,
	p PromptWriteRequest,
) (*TemplateSection, error) {
	if current == nil || !p.ContentSet {
		return nil, nil
	}
	sections, err := store.ListSectionsByTemplateID(ctx, current.ID)
	if err != nil {
		return nil, err
	}
	target := promptContentSectionForWrite(*current, sections)
	if target == nil && promptTemplateRequiresSectionContent(*current, sections) {
		return nil, fmt.Errorf("dashboard: prompt %q has no editable runtime content section", current.PromptKey)
	}
	return target, nil
}

// promptContentSectionForWrite 按模板 intent 选择最适合作为正文编辑入口的 section。
func promptContentSectionForWrite(
	template Template,
	sections []TemplateSection,
) *TemplateSection {
	if len(sections) == 0 {
		return nil
	}
	switch promptTemplateIntentKindWithSections(template, sections) {
	case "recall":
		return firstPromptSectionMatching(sections, func(section TemplateSection) bool {
			return strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall")
		})
	case "default_rule":
		if section := firstPromptSectionMatching(sections, func(section TemplateSection) bool {
			return strings.TrimSpace(section.SectionKey) == "project_rule"
		}); section != nil {
			return section
		}
		return firstPromptSectionMatching(sections, promptSectionIsDirectlyInjectable)
	case "expert":
		if section := firstPromptSectionMatching(sections, func(section TemplateSection) bool {
			return strings.TrimSpace(section.SectionKey) == "workflow"
		}); section != nil {
			return section
		}
	}
	return firstPromptSectionMatching(sections, promptSectionIsDirectlyInjectable)
}

// promptTemplateRequiresSectionContent 判断模板是否必须通过 section 保存正文。
func promptTemplateRequiresSectionContent(template Template, sections []TemplateSection) bool {
	switch promptTemplateIntentKindWithSections(template, sections) {
	case "recall", "default_rule":
		return true
	default:
		return false
	}
}

// promptTemplateIntentKind 从 agent key 或 intent tag 识别模板类型。
func promptTemplateIntentKind(template Template) string {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return "default_rule"
	}
	for _, tag := range promptTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case "intent:expert":
			return "expert"
		case "intent:recall":
			return "recall"
		case "intent:default_rule":
			return "default_rule"
		}
	}
	return ""
}

// promptTemplateIntentKindWithSections 优先使用模板显式 intent，缺失时从 sections 推断。
func promptTemplateIntentKindWithSections(
	template Template,
	sections []TemplateSection,
) string {
	if kind := promptTemplateIntentKind(template); kind != "" {
		return kind
	}
	return promptSectionsInferredIntentKind(sections)
}

// promptSectionsInferredIntentKind 从旧式 section-only 形态推断 recall 模板。
// 只要存在普通可注入正文，就不再推断为 recall，避免误改普通模板。
func promptSectionsInferredIntentKind(sections []TemplateSection) string {
	hasRecallContent := false
	for _, section := range sections {
		if section.Enabled && promptSectionIsDirectlyInjectable(section) && strings.TrimSpace(section.Body) != "" {
			return ""
		}
		if kind := promptSectionInferredIntentKind(section); kind != "" {
			hasRecallContent = true
		}
	}
	if hasRecallContent {
		return "recall"
	}
	return ""
}

// promptSectionInferredIntentKind 识别单个启用 recall section 是否足以代表 recall intent。
func promptSectionInferredIntentKind(section TemplateSection) string {
	if !section.Enabled {
		return ""
	}
	if !strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall") {
		return ""
	}
	if strings.TrimSpace(section.RecallTopic) == "" && strings.TrimSpace(section.Body) == "" {
		return ""
	}
	return "recall"
}

// promptTemplateWithInferredSectionIntent 为缺少显式 intent 的 section-only 模板补充临时 intent tag。
func promptTemplateWithInferredSectionIntent(
	template Template,
	sections []TemplateSection,
) Template {
	kind := promptSectionsInferredIntentKind(sections)
	if kind == "" || promptTemplateIntentKind(template) != "" {
		return template
	}
	template.Tags = withPromptInferredIntentTag(template.Tags, kind)
	return template
}

// withPromptInferredIntentTag 只在 tags 中没有 intent 标记时追加推断结果。
func withPromptInferredIntentTag(raw json.RawMessage, kind string) json.RawMessage {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return raw
	}
	tags := promptTags(raw)
	for _, tag := range tags {
		if strings.HasPrefix(strings.TrimSpace(tag), "intent:") {
			return raw
		}
	}
	tags = append(tags, "intent:"+kind)
	encoded, err := json.Marshal(tags)
	if err != nil {
		return raw
	}
	return json.RawMessage(encoded)
}

// firstPromptSectionMatching 返回第一个匹配 section 的副本，避免调用方修改原切片元素。
func firstPromptSectionMatching(
	sections []TemplateSection,
	match func(TemplateSection) bool,
) *TemplateSection {
	for _, section := range sections {
		if match(section) {
			copy := section
			return &copy
		}
	}
	return nil
}

// promptSectionIsDirectlyInjectable 判断 section 是否可作为普通 prompt 正文展示。
func promptSectionIsDirectlyInjectable(section TemplateSection) bool {
	return !strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall")
}

// promptAssetDraftCard 是草稿 GeneratedCard JSON 的最小 wire 形状。
// 资产列表只需要标题、摘要和正文预览字段，未知字段由原始 JSON 保留在 store 中。
type promptAssetDraftCard struct {
	Kind            string `json:"kind"`
	Title           string `json:"title"`
	Summary         string `json:"summary"`
	WhenToUse       string `json:"when_to_use,omitempty"`
	RecallBody      string `json:"recall_body,omitempty"`
	DefaultRuleBody string `json:"default_rule_body,omitempty"`
	Output          string `json:"output,omitempty"`
}

// promptAssetDraftIssue 是草稿 issues JSON 的最小 wire 形状，用于资产列表展示阻断/复核原因。
type promptAssetDraftIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// promptAssetItemsFromDrafts 将可保存草稿转为前端资产列表项。
func promptAssetItemsFromDrafts(drafts []IntentDraft) []promptAssetRPCItem {
	items := make([]promptAssetRPCItem, 0, len(drafts))
	for _, draft := range drafts {
		items = append(items, promptAssetItemFromDraft(draft))
	}
	return items
}

// promptAssetItemFromDraft 将单个 intent 草稿映射为待确认资产项。
func promptAssetItemFromDraft(draft IntentDraft) promptAssetRPCItem {
	card, cardPayload := promptAssetDraftCardPayload(draft)
	issues := []promptAssetDraftIssue{}
	_ = json.Unmarshal(draft.Issues, &issues)
	kind := promptAssetDraftKind(draft, card)
	scope := "project"
	if strings.TrimSpace(draft.Scope) == "global" {
		scope = "global"
	}
	return promptAssetRPCItem{
		promptRPCItem: promptRPCItem{
			ID:          strings.TrimSpace(draft.DraftKey),
			Name:        firstNonEmpty(card.Title, strings.TrimSpace(draft.DraftKey)),
			Content:     firstNonEmpty(card.RecallBody, card.DefaultRuleBody, card.Output, card.Summary, draft.RawInput),
			Description: strings.TrimSpace(card.Summary),
			AgentType:   promptAssetDraftAgentType(kind),
			WhenToUse:   strings.TrimSpace(card.WhenToUse),
			CreatedAt:   draft.CreatedAt,
			UpdatedAt:   draft.UpdatedAt,
			Enabled:     false,
			Scope:       scope,
			Tags:        promptAssetDraftTags(kind),
		},
		State:       "pending_confirm",
		DraftKey:    strings.TrimSpace(draft.DraftKey),
		DraftStatus: strings.TrimSpace(draft.Status),
		SourceType:  strings.TrimSpace(draft.SourceType),
		Card:        cardPayload,
		Issues:      issues,
	}
}

// promptAssetDraftCardPayload 解析并规范化草稿卡片，同时返回结构化 payload 供前端展示。
func promptAssetDraftCardPayload(draft IntentDraft) (promptAssetDraftCard, map[string]any) {
	generated := promptintent.Card{}
	if err := json.Unmarshal(draft.GeneratedCard, &generated); err == nil {
		normalized := promptintent.NormalizeGeneratedCard(draft.Kind, draft.RawInput, generated)
		if raw, marshalErr := json.Marshal(normalized); marshalErr == nil {
			card := promptAssetDraftCard{}
			_ = json.Unmarshal(raw, &card)
			payload := map[string]any{}
			_ = json.Unmarshal(raw, &payload)
			return card, payload
		}
	}
	card := promptAssetDraftCard{}
	_ = json.Unmarshal(draft.GeneratedCard, &card)
	payload := map[string]any{}
	_ = json.Unmarshal(draft.GeneratedCard, &payload)
	return card, payload
}

// promptAssetDraftKind 优先使用卡片 kind，其次使用草稿 kind，最终按 expert 展示。
func promptAssetDraftKind(draft IntentDraft, card promptAssetDraftCard) string {
	return firstNonEmpty(card.Kind, draft.Kind, "expert")
}

// promptAssetDraftAgentType 把 default_rule 草稿映射到专用 agent type，其余走 main。
func promptAssetDraftAgentType(kind string) string {
	if strings.TrimSpace(kind) == "default_rule" {
		return "default_rule"
	}
	return promptDefaultAgent
}

// promptAssetDraftTags 为草稿资产生成 intent tag，序列化失败时返回 nil。
func promptAssetDraftTags(kind string) json.RawMessage {
	encoded, err := json.Marshal([]string{"intent:" + strings.TrimSpace(kind)})
	if err != nil {
		return nil
	}
	return encoded
}
