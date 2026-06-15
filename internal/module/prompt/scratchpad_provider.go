package prompt

import (
	"context"
	"fmt"
	"strings"
)

var _ DynamicSectionProvider = ScratchpadProvider{}

type ScratchpadProvider struct{}

// SectionName 处理section名称。
func (ScratchpadProvider) SectionName() string {
	return DynamicSectionScratchpad
}

// Resolve 解析prompt。
func (ScratchpadProvider) Resolve(_ context.Context, input SectionContext) (*string, error) {
	dir := strings.TrimSpace(input.BuildCtx.ScratchpadDir)
	if dir == "" {
		return nil, nil
	}
	text := strings.TrimSpace(fmt.Sprintf(
		"# Scratchpad Directory\n\n"+
			"IMPORTANT: Always use this scratchpad directory for temporary files instead of `/tmp` or other system temp directories:\n"+
			"`%s`\n\n"+
			"Use this directory for ALL temporary file needs:\n"+
			"- Storing intermediate results or data during multi-step tasks\n"+
			"- Writing temporary scripts or configuration files\n"+
			"- Saving outputs that don't belong in the user's project\n"+
			"- Creating working files during analysis or processing\n"+
			"- Any file that would otherwise go to `/tmp`\n\n"+
			"Only use `/tmp` if the user explicitly requests it.\n\n"+
			"The scratchpad directory is session-specific and isolated from the user's project.",
		dir,
	))
	return &text, nil
}
