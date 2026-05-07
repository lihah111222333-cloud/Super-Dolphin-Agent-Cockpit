package prompt

import (
	"context"
	"fmt"
	"strings"
)

const briefSectionText = `# Brief Mode
- Brief mode is enabled. Keep user-visible replies compact, outcome-first, and free of filler.
- Prefer one short paragraph or a tight bullet list unless more detail is required to complete the task safely.
- If you need tool work, acknowledge briefly, do the work, then return the result.
- Apply this together with Output Style and output_efficiency; if they differ, follow the stricter brevity constraint without contradicting the requested style.`

const summarizeToolResultsSectionText = `When working with tool results, write down any important information you might need later in your response, as the original tool result may be cleared later.`

var _ DynamicSectionProvider = BriefProvider{}
var _ DynamicSectionProvider = SummarizeToolResultsProvider{}

type BriefProvider struct{}
type SummarizeToolResultsProvider struct{}

func (BriefProvider) SectionName() string {
	return DynamicSectionBrief
}

func (BriefProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	if !briefEnabled(input.BuildCtx) {
		return nil, nil
	}
	text := briefSectionText
	return &text, nil
}

func (SummarizeToolResultsProvider) SectionName() string {
	return DynamicSectionSummarizeToolResults
}

func (SummarizeToolResultsProvider) Resolve(context.Context, SectionContext) (*string, error) {
	text := summarizeToolResultsSectionText
	return &text, nil
}

func briefEnabled(build BuildCtx) bool {
	return briefFeatureEnabled(build) && briefModeEnabled(build)
}

func briefFeatureEnabled(build BuildCtx) bool {
	return promptFeatureEnabled(build.SessionFlags, []string{"KAIROS", "KAIROS_BRIEF"}, "memory_kairos", "kairos", "kairos_brief", "kairosBrief")
}

func briefModeEnabled(build BuildCtx) bool {
	if promptFlagEnabled(build.SessionFlags, "brief", "brief_enabled", "isBriefEnabled", "is_brief_enabled") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(build.Summary), "brief")
}

// ---------------------------------------------------------------------------
// LanguageProvider (was language_provider.go)
// ---------------------------------------------------------------------------

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
