package prompt

import (
	"context"
	"strings"
)

const briefSectionText = `# Brief Mode
- Brief mode is enabled. Keep user-visible replies compact, outcome-first, and free of filler.
- Prefer one short paragraph or a tight bullet list unless more detail is required to complete the task safely.
- If you need tool work, acknowledge briefly, do the work, then return the result.
- Apply this together with Output Style and output_efficiency; if they differ, follow the stricter brevity constraint without contradicting the requested style.`

var _ DynamicSectionProvider = BriefProvider{}

type BriefProvider struct{}

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
