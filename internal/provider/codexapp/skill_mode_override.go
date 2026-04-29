package codexapp

import (
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

// overrideSkillsToSummary 把 codexapp 这条 provider 路径上的 SkillRef 默认注入模式
// 从 Unspecified（隐式默认）翻成 Summary，开启 progressive-disclosure：模型从 system
// prompt 拿到 skill 摘要 + skill_expand_body 工具指针，再按需调用 toolbridge
// host-direct 分支拉取完整 body。
//
// 设计（P20.18 Phase 1.5 v2 / B 路）：
//   - **只默认翻 Unspecified**：caller 未明确表态 mode 时才介入。
//   - 显式 Full / Summary / None 一律尊重：P20.2 §4 以及其它调用方期望明确
//     mode 语义的，在本函数不被默默覆写。这避免了 v1 envelope 渲染、本地测试绕
//     开 progressive-disclosure 等场景被意外吃掉。
//   - claudecli 路径不调本函数，保持原始行为，避免 Phase 2 完成前丢 trust=project body。
//   - Phase 2（claudecli skill stdio MCP server）落地后，turn.DefaultSkillMode() 可统一翻为
//     Summary，本函数应被删除（grep "overrideSkillsToSummary" 应零命中）。
//
// 该函数返回新切片，不修改入参。
func overrideSkillsToSummary(in []dto.SkillRef) []dto.SkillRef {
	if len(in) == 0 {
		return in
	}
	out := make([]dto.SkillRef, len(in))
	for i, ref := range in {
		// 只覆盖 Unspecified（零值）；caller 显式设了 Full / Summary / None 一律尊重原意。
		if ref.Mode != dto.SkillModeUnspecified {
			out[i] = ref
			continue
		}
		ref.Mode = dto.SkillModeSummary
		out[i] = ref
	}
	return out
}
