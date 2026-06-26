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

// BriefProvider 在 brief 模式启用时注入简洁回复约束。
type BriefProvider struct{}

// SummarizeToolResultsProvider 注入工具结果压缩提醒，避免长工具输出被清理后丢失关键信息。
type SummarizeToolResultsProvider struct{}

// SectionName 返回 brief 模式使用的动态 prompt slot。
func (BriefProvider) SectionName() string {
	return DynamicSectionBrief
}

// Resolve 在会话标志或 summary 明确启用 brief 时返回 brief 指令。
func (BriefProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	if !briefEnabled(input.BuildCtx) {
		return nil, nil
	}
	text := briefSectionText
	return &text, nil
}

// SectionName 返回工具结果摘要提醒使用的动态 prompt slot。
func (SummarizeToolResultsProvider) SectionName() string {
	return DynamicSectionSummarizeToolResults
}

// Resolve 始终返回工具结果摘要提醒；该 section 不依赖输入上下文。
func (SummarizeToolResultsProvider) Resolve(context.Context, SectionContext) (*string, error) {
	text := summarizeToolResultsSectionText
	return &text, nil
}

// briefEnabled 判断当前 BuildCtx 是否同时打开 brief 功能和 brief 模式。
func briefEnabled(build BuildCtx) bool {
	return briefFeatureEnabled(build) && briefModeEnabled(build)
}

// briefFeatureEnabled 判断会话功能开关是否允许 brief provider 生效。
func briefFeatureEnabled(build BuildCtx) bool {
	return promptFeatureEnabled(build.SessionFlags, []string{"KAIROS", "KAIROS_BRIEF"}, "memory_kairos", "kairos", "kairos_brief", "kairosBrief")
}

// briefModeEnabled 判断当前 turn 是否请求 brief 输出模式。
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

// LanguageProvider 注入默认回复语言规则；它不读取用户资料，只依赖 BuildCtx.Language 覆盖。
type LanguageProvider struct{}

// languageDefaultSectionText 是未显式配置语言时的默认回复语言约束。
// 产品默认中文，但用户单轮明确指定其他语言时仍允许覆盖。
const languageDefaultSectionText = `# Language
- Default to Chinese for all user-visible replies when no explicit language is configured.
- If the user's latest message explicitly requests another language, follow that requested language for that reply.
- Keep one single language consistent across the entire reply, including narration, headings, and lists. Do not mix languages or drift between them mid-response.
- Technical terms, code identifiers, file paths, and command names stay in their original form.`

// SectionName 返回语言约束使用的动态 prompt slot。
func (LanguageProvider) SectionName() string {
	return DynamicSectionLanguage
}

// Resolve 根据 BuildCtx.Language 生成语言约束；未配置时返回产品默认中文规则。
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
