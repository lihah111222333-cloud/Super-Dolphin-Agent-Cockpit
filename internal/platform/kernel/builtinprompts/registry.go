package builtinprompts

import (
	"encoding/json"
	"sort"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// Registry indexes embedded prompt templates and their sections.
type Registry struct {
	templates            []contract.BuiltinPromptTemplate
	templatesByPromptKey map[string]contract.BuiltinPromptTemplate
	sectionsByTemplateID map[int64][]contract.BuiltinPromptSection
}

func newRegistry(loaded []loadedTemplate) *Registry {
	sort.SliceStable(loaded, loadedTemplateLess(loaded))

	registry := &Registry{
		templates:            make([]contract.BuiltinPromptTemplate, 0, len(loaded)),
		templatesByPromptKey: map[string]contract.BuiltinPromptTemplate{},
		sectionsByTemplateID: map[int64][]contract.BuiltinPromptSection{},
	}
	nextSectionID := firstBuiltinSectionID
	for i, item := range loaded {
		templateID := builtinTemplateID(item.Config, i)
		template := buildTemplate(templateID, item.Config)
		registry.templates = append(registry.templates, template)
		registry.templatesByPromptKey[template.PromptKey] = template
		registry.sectionsByTemplateID[templateID] = buildSections(templateID, &nextSectionID, item.Sections)
	}
	return registry
}

// loadedTemplateLess 处理loadedtemplateless。
func loadedTemplateLess(loaded []loadedTemplate) func(i, j int) bool {
	return func(i, j int) bool {
		left, right := loaded[i].Config, loaded[j].Config
		if left.ID != nil && right.ID != nil && *left.ID != *right.ID {
			return *left.ID > *right.ID
		}
		if left.ID != nil && right.ID == nil {
			return true
		}
		if left.ID == nil && right.ID != nil {
			return false
		}
		return left.PromptKey < right.PromptKey
	}
}

func builtinTemplateID(cfg templateConfig, index int) int64 {
	if cfg.ID != nil {
		return *cfg.ID
	}
	return firstBuiltinID - int64(index)
}

func buildTemplate(id int64, cfg templateConfig) contract.BuiltinPromptTemplate {
	return contract.BuiltinPromptTemplate{
		ID:          id,
		PromptKey:   cfg.PromptKey,
		Kind:        cfg.Kind,
		Title:       cfg.Title,
		AgentKey:    cfg.AgentKey,
		ToolName:    cfg.ToolName,
		PromptText:  cfg.PromptText,
		WhenToUse:   cfg.WhenToUse,
		Description: cfg.Description,
		Tags:        copyStrings(cfg.Tags),
		Enabled:     boolValue(cfg.Enabled, false),
		Scope:       cfg.Scope,
		MatchWhen:   copyRawJSON(cfg.MatchWhen),
		Priority:    cfg.Priority,
	}
}

func buildSections(templateID int64, nextID *int64, loaded []loadedSection) []contract.BuiltinPromptSection {
	sections := make([]contract.BuiltinPromptSection, 0, len(loaded))
	for _, item := range loaded {
		sections = append(sections, buildSection(templateID, *nextID, item))
		*nextID = *nextID - 1
	}
	return sections
}

func buildSection(templateID, id int64, item loadedSection) contract.BuiltinPromptSection {
	cfg := item.Config
	return contract.BuiltinPromptSection{
		ID:          id,
		TemplateID:  templateID,
		SectionKey:  cfg.SectionKey,
		Region:      cfg.Region,
		Ordinal:     cfg.Ordinal,
		Body:        item.Body,
		EnableWhen:  copyRawJSON(cfg.EnableWhen),
		Enabled:     boolValue(cfg.Enabled, true),
		TriggerType: cfg.TriggerType,
		RecallTopic: cfg.RecallTopic,
	}
}

// ListTemplates 列出templates。
func (r *Registry) ListTemplates() []contract.BuiltinPromptTemplate {
	out := make([]contract.BuiltinPromptTemplate, len(r.templates))
	copy(out, r.templates)
	for i := range out {
		out[i].Tags = copyStrings(out[i].Tags)
		out[i].MatchWhen = copyRawJSON(out[i].MatchWhen)
	}
	return out
}

// GetTemplate 读取template。
func (r *Registry) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	template, ok := r.templatesByPromptKey[promptKey]
	if !ok {
		return contract.BuiltinPromptTemplate{}, false
	}
	template.Tags = copyStrings(template.Tags)
	template.MatchWhen = copyRawJSON(template.MatchWhen)
	return template, true
}

// SectionsByTemplateID 按templateID处理sections。
func (r *Registry) SectionsByTemplateID(templateID int64) []contract.BuiltinPromptSection {
	sections := r.sectionsByTemplateID[templateID]
	out := make([]contract.BuiltinPromptSection, len(sections))
	copy(out, sections)
	for i := range out {
		out[i].EnableWhen = copyRawJSON(out[i].EnableWhen)
	}
	return out
}

func copyStrings(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyRawJSON(in json.RawMessage) json.RawMessage {
	if in == nil {
		return nil
	}
	out := make(json.RawMessage, len(in))
	copy(out, in)
	return out
}

func boolValue(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}
