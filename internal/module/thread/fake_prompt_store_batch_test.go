package thread

import (
	"context"

	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

func (f *fakePromptStore) ListSectionsByTemplateIDs(_ context.Context, templateIDs []int64) ([]promptstore.PromptTemplateSection, error) {
	out := make([]promptstore.PromptTemplateSection, 0)
	for _, id := range templateIDs {
		out = append(out, f.sectionsByTemplateID[id]...)
	}
	return append([]promptstore.PromptTemplateSection(nil), out...), nil
}
