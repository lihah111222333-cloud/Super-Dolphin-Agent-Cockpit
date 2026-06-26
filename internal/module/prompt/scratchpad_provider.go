package prompt

import (
	"context"
	"fmt"
	"strings"
)

var _ DynamicSectionProvider = ScratchpadProvider{}

// ScratchpadProvider 注入会话专属临时目录，避免代理把中间产物写进系统 /tmp 或项目目录。
type ScratchpadProvider struct{}

// SectionName 返回 scratchpad 动态 section 的注册名。
func (ScratchpadProvider) SectionName() string {
	return DynamicSectionScratchpad
}

// Resolve 仅在 BuildCtx 提供 ScratchpadDir 时渲染提示；目录缺失表示该运行面未启用 scratchpad。
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
