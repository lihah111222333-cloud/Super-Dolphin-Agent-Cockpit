package threadprompt

import (
	"context"
	"fmt"
	"strings"

	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// storeTemplatesWithInferredSectionIntents 批量推断一组模板的 intent tag，只对缺少 intent 且有 ID 的模板发起 section 查询。
func (c *runtimePromptCatalog) storeTemplatesWithInferredSectionIntents(
	ctx context.Context,
	templates []promptstore.PromptTemplate,
) ([]promptstore.PromptTemplate, error) {
	if len(templates) == 0 {
		return templates, nil
	}
	templateIDs := runtimeTemplateIDsNeedingIntentInference(templates)
	if len(templateIDs) == 0 {
		return templates, nil
	}
	sections, err := c.store.ListSectionsByTemplateIDs(ctx, templateIDs)
	if err != nil {
		return nil, fmt.Errorf("runtime prompt catalog: list prompt_template_sections: %w", err)
	}
	sectionsByTemplateID := runtimeSectionsByTemplateID(sections)
	out := make([]promptstore.PromptTemplate, len(templates))
	for i, template := range templates {
		out[i] = runtimeTemplateWithInferredSectionIntent(template, sectionsByTemplateID[template.ID])
	}
	return out, nil
}

// storeTemplateWithInferredSectionIntent 对单个模板推断 intent tag，若已有 intent 则直接返回原值。
func (c *runtimePromptCatalog) storeTemplateWithInferredSectionIntent(
	ctx context.Context,
	template promptstore.PromptTemplate,
) (promptstore.PromptTemplate, error) {
	if !runtimeTemplateNeedsSectionIntentInference(template) {
		return template, nil
	}
	sections, err := c.store.ListSectionsByTemplateID(ctx, template.ID)
	if err != nil {
		return promptstore.PromptTemplate{}, fmt.Errorf("runtime prompt catalog: list prompt_template_sections for %q: %w", template.PromptKey, err)
	}
	return runtimeTemplateWithInferredSectionIntent(template, sections), nil
}

// runtimeTemplateIDsNeedingIntentInference 筛选出需要推断 intent 的模板 ID 列表。
func runtimeTemplateIDsNeedingIntentInference(templates []promptstore.PromptTemplate) []int64 {
	ids := make([]int64, 0, len(templates))
	for _, template := range templates {
		if runtimeTemplateNeedsSectionIntentInference(template) {
			ids = append(ids, template.ID)
		}
	}
	return ids
}

// runtimeTemplateNeedsSectionIntentInference 判断模板是否需要通过 section 内容推断 intent（有 ID 且无 intent tag）。
func runtimeTemplateNeedsSectionIntentInference(template promptstore.PromptTemplate) bool {
	return template.ID > 0 && runtimeTemplateIntentKind(template) == ""
}

// runtimeTemplateIntentKind 从 agent_key 或 tags 中提取 intent 类型字符串，未找到返回空字符串。
func runtimeTemplateIntentKind(template promptstore.PromptTemplate) string {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return "default_rule"
	}
	for _, tag := range promptstore.TemplateTags(template.Tags) {
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

// runtimeSectionsByTemplateID 将 sections 按 template_id 分组，供批量推断时快速查找。
func runtimeSectionsByTemplateID(sections []promptstore.PromptTemplateSection) map[int64][]promptstore.PromptTemplateSection {
	byTemplateID := make(map[int64][]promptstore.PromptTemplateSection)
	for _, section := range sections {
		byTemplateID[section.TemplateID] = append(byTemplateID[section.TemplateID], section)
	}
	return byTemplateID
}

// runtimeTemplateWithInferredSectionIntent 根据 section 内容给模板追加推断出的 intent tag；若已有 intent 或无法推断则返回原值。
func runtimeTemplateWithInferredSectionIntent(
	template promptstore.PromptTemplate,
	sections []promptstore.PromptTemplateSection,
) promptstore.PromptTemplate {
	if runtimeTemplateIntentKind(template) != "" {
		return template
	}
	kind := runtimeSectionsInferredIntentKind(sections)
	if kind == "" {
		return template
	}
	tags := promptstore.TemplateTags(template.Tags)
	template.Tags = runtimeEncodeTags(runtimeAppendTagIfMissing(tags, "intent:"+kind))
	return template
}

// runtimeSectionsInferredIntentKind 扫描 sections，若全部为 recall 类型（无可直接注入的 body）则返回 "recall"，否则返回空字符串。
func runtimeSectionsInferredIntentKind(sections []promptstore.PromptTemplateSection) string {
	hasRecallContent := false
	for _, section := range sections {
		if section.Enabled && runtimeSectionIsDirectlyInjectable(section) && strings.TrimSpace(section.Body) != "" {
			return ""
		}
		if runtimeSectionInferredIntentKind(section) != "" {
			hasRecallContent = true
		}
	}
	if hasRecallContent {
		return "recall"
	}
	return ""
}

// runtimeSectionInferredIntentKind 判断单个 section 是否为有效 recall section，是则返回 "recall"，否则返回空字符串。
func runtimeSectionInferredIntentKind(section promptstore.PromptTemplateSection) string {
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

// runtimeSectionIsDirectlyInjectable 判断 section 是否可直接注入（trigger_type 不是 recall）。
func runtimeSectionIsDirectlyInjectable(section promptstore.PromptTemplateSection) bool {
	return !strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall")
}
