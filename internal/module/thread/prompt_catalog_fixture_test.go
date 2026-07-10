package thread

import (
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func newBuiltinPromptCatalogForTest(registry contract.BuiltinPromptRegistry) *fakePromptCatalog {
	catalog := &fakePromptCatalog{
		sectionsByTemplateID: make(map[int64][]PromptTemplateSection),
		readOnly:             true,
	}
	if registry == nil {
		return catalog
	}
	for _, template := range registry.ListTemplates() {
		tags, _ := json.Marshal(template.Tags)
		catalog.templates = append(catalog.templates, PromptTemplate{
			ID: template.ID, PromptKey: template.PromptKey, Title: template.Title,
			AgentKey: template.AgentKey, ToolName: template.ToolName, PromptText: template.PromptText,
			WhenToUse: template.WhenToUse, Description: template.Description, Tags: tags,
			Enabled: template.Enabled, MatchWhen: append(json.RawMessage(nil), template.MatchWhen...),
			Priority: template.Priority,
		})
		for _, section := range registry.SectionsByTemplateID(template.ID) {
			catalog.sectionsByTemplateID[template.ID] = append(catalog.sectionsByTemplateID[template.ID], PromptTemplateSection{
				ID: section.ID, TemplateID: section.TemplateID, SectionKey: section.SectionKey,
				Region: section.Region, Ordinal: section.Ordinal, Body: section.Body,
				EnableWhen: append(json.RawMessage(nil), section.EnableWhen...), Enabled: section.Enabled,
				TriggerType: section.TriggerType, RecallTopic: section.RecallTopic,
			})
		}
	}
	return catalog
}
