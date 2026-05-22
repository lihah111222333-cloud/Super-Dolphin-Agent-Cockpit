package threadprompt

import promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"

func fakePromptSectionsByTemplateIDs(
	byTemplateID map[int64][]promptstore.PromptTemplateSection,
	templateIDs []int64,
) []promptstore.PromptTemplateSection {
	out := make([]promptstore.PromptTemplateSection, 0)
	for _, id := range templateIDs {
		out = append(out, byTemplateID[id]...)
	}
	return append([]promptstore.PromptTemplateSection(nil), out...)
}
