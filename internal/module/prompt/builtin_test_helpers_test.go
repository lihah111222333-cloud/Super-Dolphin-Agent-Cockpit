package prompt

import "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

type fakeBuiltinPromptRegistry struct {
	templates []contract.BuiltinPromptTemplate
	sections  map[int64][]contract.BuiltinPromptSection
}

func fakeBuiltinRegistryWithKeys(keys ...string) *fakeBuiltinPromptRegistry {
	templates := make([]contract.BuiltinPromptTemplate, 0, len(keys))
	for i, key := range keys {
		templates = append(templates, contract.BuiltinPromptTemplate{
			ID:        int64(-100000 - i),
			PromptKey: key,
			Kind:      "base",
			Title:     key,
			AgentKey:  "main",
			Enabled:   true,
			Scope:     "global",
			Tags:      []string{"builtin:system"},
		})
	}
	return &fakeBuiltinPromptRegistry{templates: templates}
}

func (r *fakeBuiltinPromptRegistry) ListTemplates() []contract.BuiltinPromptTemplate {
	out := make([]contract.BuiltinPromptTemplate, len(r.templates))
	copy(out, r.templates)
	return out
}

func (r *fakeBuiltinPromptRegistry) GetTemplate(promptKey string) (contract.BuiltinPromptTemplate, bool) {
	for _, template := range r.templates {
		if template.PromptKey == promptKey {
			return template, true
		}
	}
	return contract.BuiltinPromptTemplate{}, false
}

func (r *fakeBuiltinPromptRegistry) SectionsByTemplateID(templateID int64) []contract.BuiltinPromptSection {
	sections := r.sections[templateID]
	out := make([]contract.BuiltinPromptSection, len(sections))
	copy(out, sections)
	return out
}
