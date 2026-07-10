package threadprompt

func fakePromptSectionsByTemplateIDs(
	byTemplateID map[int64][]PromptTemplateSection,
	templateIDs []int64,
) []PromptTemplateSection {
	out := make([]PromptTemplateSection, 0)
	for _, id := range templateIDs {
		out = append(out, byTemplateID[id]...)
	}
	return append([]PromptTemplateSection(nil), out...)
}
