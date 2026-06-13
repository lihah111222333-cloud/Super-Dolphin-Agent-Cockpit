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

// SectionName 处理section名称。
func (BriefProvider) SectionName() string {
	return DynamicSectionBrief
}

// Resolve 解析prompt。
func (BriefProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	if !briefEnabled(input.BuildCtx) {
		return nil, nil
	}
	text := briefSectionText
	return &text, nil
}

// SectionName 处理section名称。
func (SummarizeToolResultsProvider) SectionName() string {
	return DynamicSectionSummarizeToolResults
}

// Resolve 解析prompt。
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

// languageDefaultSectionText anchors reply language when no explicit
// `language` is configured. The product default is Chinese, while still
// allowing the user to explicitly request another language in a turn.
const languageDefaultSectionText = `# Language
- Default to Chinese for all user-visible replies when no explicit language is configured.
- If the user's latest message explicitly requests another language, follow that requested language for that reply.
- Keep one single language consistent across the entire reply, including narration, headings, and lists. Do not mix languages or drift between them mid-response.
- Technical terms, code identifiers, file paths, and command names stay in their original form.`

// SectionName 处理section名称。
func (LanguageProvider) SectionName() string {
	return DynamicSectionLanguage
}

// Resolve 解析prompt。
func (LanguageProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	language := strings.TrimSpace(input.BuildCtx.Language)
	if language == "" {
		text := languageDefaultSectionText
		return &text, nil
	}
	text := fmt.Sprintf(
		"# Language\nAlways respond in %s. Use %s for all explanations, comments, and communications with the user. Technical terms and code identifiers should remain in their original form.",
		language,
		language,
	)
	return &text, nil
}
