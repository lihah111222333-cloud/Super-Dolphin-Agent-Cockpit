package prompt

import (
	"context"
	"fmt"
	"strings"
)

var _ DynamicSectionProvider = LanguageProvider{}

type LanguageProvider struct{}

func (LanguageProvider) SectionName() string {
	return DynamicSectionLanguage
}

func (LanguageProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	language := strings.TrimSpace(input.BuildCtx.Language)
	if language == "" {
		return nil, nil
	}
	text := fmt.Sprintf(
		"# Language\nAlways respond in %s. Use %s for all explanations, comments, and communications with the user. Technical terms and code identifiers should remain in their original form.",
		language,
		language,
	)
	return &text, nil
}
