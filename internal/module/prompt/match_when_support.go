package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	promptintent "github.com/anthropic-ai/super-agent-v3/internal/module/prompt/intent"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// MatchWhen helper source note: these helpers support the merged
// EvaluateMatchWhen/matchWhenKeyMatches evaluator after the former match_when.go
// contents were folded into enable_when.go.
func matchWhenStringValue(want any) string {
	value, ok := want.(string)
	if !ok {
		return ""
	}
	return value
}

func matchCWDGlob(pattern string, cwd string) bool {
	if pattern == "" || cwd == "" {
		return false
	}
	matched, err := filepath.Match(filepath.ToSlash(pattern), filepath.ToSlash(cwd))
	return err == nil && matched
}

func matchCWDPrefix(prefix string, cwd string) bool {
	if prefix == "" || cwd == "" {
		return false
	}
	return strings.HasPrefix(filepath.ToSlash(cwd), filepath.ToSlash(prefix))
}

func matchTagsHas(keyword string, userPrompt string) bool {
	if keyword == "" || userPrompt == "" {
		return false
	}
	return strings.Contains(
		strings.ToLower(userPrompt),
		strings.ToLower(keyword),
	)
}

// storePromptTemplateAndContent 保存prompttemplate内容。
func storePromptTemplateAndContent(
	ctx context.Context,
	store promptstore.Store,
	template promptstore.PromptTemplate,
	current *promptstore.PromptTemplate,
	contentSection *promptstore.PromptTemplateSection,
	content string,
) (*promptstore.PromptTemplate, error) {
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

// promptContentSectionTargetForWrite 为write处理prompt内容sectiontarget。
func promptContentSectionTargetForWrite(
	ctx context.Context,
	store promptstore.Store,
	current *promptstore.PromptTemplate,
	p PromptWriteRequest,
) (*promptstore.PromptTemplateSection, error) {
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

// promptContentSectionForWrite 为write处理prompt内容section。
func promptContentSectionForWrite(
	template promptstore.PromptTemplate,
	sections []promptstore.PromptTemplateSection,
) *promptstore.PromptTemplateSection {
	if len(sections) == 0 {
		return nil
	}
	switch promptTemplateIntentKindWithSections(template, sections) {
	case "recall":
		return firstPromptSectionMatching(sections, func(section promptstore.PromptTemplateSection) bool {
			return strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall")
		})
	case "default_rule":
		if section := firstPromptSectionMatching(sections, func(section promptstore.PromptTemplateSection) bool {
			return strings.TrimSpace(section.SectionKey) == "project_rule"
		}); section != nil {
			return section
		}
		return firstPromptSectionMatching(sections, promptSectionIsDirectlyInjectable)
	case "expert":
		if section := firstPromptSectionMatching(sections, func(section promptstore.PromptTemplateSection) bool {
			return strings.TrimSpace(section.SectionKey) == "workflow"
		}); section != nil {
			return section
		}
	}
	return firstPromptSectionMatching(sections, promptSectionIsDirectlyInjectable)
}

func promptTemplateRequiresSectionContent(template promptstore.PromptTemplate, sections []promptstore.PromptTemplateSection) bool {
	switch promptTemplateIntentKindWithSections(template, sections) {
	case "recall", "default_rule":
		return true
	default:
		return false
	}
}

// promptTemplateIntentKind 处理prompttemplateintentkind。
func promptTemplateIntentKind(template promptstore.PromptTemplate) string {
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

func promptTemplateIntentKindWithSections(
	template promptstore.PromptTemplate,
	sections []promptstore.PromptTemplateSection,
) string {
	if kind := promptTemplateIntentKind(template); kind != "" {
		return kind
	}
	return promptSectionsInferredIntentKind(sections)
}

// promptSectionsInferredIntentKind 处理promptsectionsinferredintentkind。
func promptSectionsInferredIntentKind(sections []promptstore.PromptTemplateSection) string {
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

func promptSectionInferredIntentKind(section promptstore.PromptTemplateSection) string {
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

func promptTemplateWithInferredSectionIntent(
	template promptstore.PromptTemplate,
	sections []promptstore.PromptTemplateSection,
) promptstore.PromptTemplate {
	kind := promptSectionsInferredIntentKind(sections)
	if kind == "" || promptTemplateIntentKind(template) != "" {
		return template
	}
	template.Tags = withPromptInferredIntentTag(template.Tags, kind)
	return template
}

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

func firstPromptSectionMatching(
	sections []promptstore.PromptTemplateSection,
	match func(promptstore.PromptTemplateSection) bool,
) *promptstore.PromptTemplateSection {
	for _, section := range sections {
		if match(section) {
			copy := section
			return &copy
		}
	}
	return nil
}

func promptSectionIsDirectlyInjectable(section promptstore.PromptTemplateSection) bool {
	return !strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall")
}

type promptAssetDraftCard struct {
	Kind            string `json:"kind"`
	Title           string `json:"title"`
	Summary         string `json:"summary"`
	WhenToUse       string `json:"when_to_use,omitempty"`
	RecallBody      string `json:"recall_body,omitempty"`
	DefaultRuleBody string `json:"default_rule_body,omitempty"`
	Output          string `json:"output,omitempty"`
}

type promptAssetDraftIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

func promptAssetItemsFromDrafts(drafts []promptstore.PromptIntentDraft) []promptAssetRPCItem {
	items := make([]promptAssetRPCItem, 0, len(drafts))
	for _, draft := range drafts {
		items = append(items, promptAssetItemFromDraft(draft))
	}
	return items
}

// promptAssetItemFromDraft 从draft处理promptassetitem。
func promptAssetItemFromDraft(draft promptstore.PromptIntentDraft) promptAssetRPCItem {
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

func promptAssetDraftCardPayload(draft promptstore.PromptIntentDraft) (promptAssetDraftCard, map[string]any) {
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

func promptAssetDraftKind(draft promptstore.PromptIntentDraft, card promptAssetDraftCard) string {
	return firstNonEmpty(card.Kind, draft.Kind, "expert")
}

func promptAssetDraftAgentType(kind string) string {
	if strings.TrimSpace(kind) == "default_rule" {
		return "default_rule"
	}
	return promptDefaultAgent
}

func promptAssetDraftTags(kind string) json.RawMessage {
	encoded, err := json.Marshal([]string{"intent:" + strings.TrimSpace(kind)})
	if err != nil {
		return nil
	}
	return encoded
}
