package threadprompt

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func (c *runtimePromptCatalog) storeTemplatesWithInferredSectionIntents(
	ctx context.Context,
	templates []contract.PromptTemplate,
) ([]contract.PromptTemplate, error) {
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
	out := make([]contract.PromptTemplate, len(templates))
	for i, template := range templates {
		out[i] = runtimeTemplateWithInferredSectionIntent(template, sectionsByTemplateID[template.ID])
	}
	return out, nil
}

func (c *runtimePromptCatalog) storeTemplateWithInferredSectionIntent(
	ctx context.Context,
	template contract.PromptTemplate,
) (contract.PromptTemplate, error) {
	if !runtimeTemplateNeedsSectionIntentInference(template) {
		return template, nil
	}
	sections, err := c.store.ListSectionsByTemplateID(ctx, template.ID)
	if err != nil {
		return contract.PromptTemplate{}, fmt.Errorf("runtime prompt catalog: list prompt_template_sections for %q: %w", template.PromptKey, err)
	}
	return runtimeTemplateWithInferredSectionIntent(template, sections), nil
}

func runtimeTemplateIDsNeedingIntentInference(templates []contract.PromptTemplate) []int64 {
	ids := make([]int64, 0, len(templates))
	for _, template := range templates {
		if runtimeTemplateNeedsSectionIntentInference(template) {
			ids = append(ids, template.ID)
		}
	}
	return ids
}

func runtimeTemplateNeedsSectionIntentInference(template contract.PromptTemplate) bool {
	return template.ID > 0 && runtimeTemplateIntentKind(template) == ""
}

// runtimeTemplateIntentKind 处理运行时templateintentkind。
func runtimeTemplateIntentKind(template contract.PromptTemplate) string {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return "default_rule"
	}
	for _, tag := range contract.PromptTemplateTags(template.Tags) {
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

func runtimeSectionsByTemplateID(sections []contract.PromptTemplateSection) map[int64][]contract.PromptTemplateSection {
	byTemplateID := make(map[int64][]contract.PromptTemplateSection)
	for _, section := range sections {
		byTemplateID[section.TemplateID] = append(byTemplateID[section.TemplateID], section)
	}
	return byTemplateID
}

func runtimeTemplateWithInferredSectionIntent(
	template contract.PromptTemplate,
	sections []contract.PromptTemplateSection,
) contract.PromptTemplate {
	if runtimeTemplateIntentKind(template) != "" {
		return template
	}
	kind := runtimeSectionsInferredIntentKind(sections)
	if kind == "" {
		return template
	}
	tags := contract.PromptTemplateTags(template.Tags)
	template.Tags = runtimeEncodeTags(runtimeAppendTagIfMissing(tags, "intent:"+kind))
	return template
}

// runtimeSectionsInferredIntentKind 处理运行时sectionsinferredintentkind。
func runtimeSectionsInferredIntentKind(sections []contract.PromptTemplateSection) string {
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

func runtimeSectionInferredIntentKind(section contract.PromptTemplateSection) string {
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

func runtimeSectionIsDirectlyInjectable(section contract.PromptTemplateSection) bool {
	return !strings.EqualFold(strings.TrimSpace(section.TriggerType), "recall")
}
