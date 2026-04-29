package nativefilter

import (
	"encoding/json"
	"fmt"
)

// claudeSettings 是 BuildClaudeSettings 的输出 JSON 结构。
// 仅含 P5 用到的字段（permissions.deny）；未来扩 enabledPlugins 等再加。
type claudeSettings struct {
	Permissions claudePermissions `json:"permissions"`
}

type claudePermissions struct {
	Deny []string `json:"deny"`
}

// BuildClaudeSettings 把 base.Claude 的 disabled_tools + disabled_skills +
// extra（来自 SkillMeta.ReplacesNative 聚合）渲染成 Claude Code settings.json。
//
// disabled_skills 与 extra 都用 "Skill:<name>" 冒号格式 wrap（2026-04-30 实测
// 确认的正确语法；spec §8.3 文本里的 "Skill(name)" 圆括号实测不生效，待 errata）。
// disabled_tools 直接进 deny（保留 base 字面，例如 "Read" / "Bash(git *)"）。
// 输出列表去重 + 保持稳定顺序：disabled_tools 优先，然后 disabled_skills，
// 最后 extra；同一字符串只出现一次。
//
// 空输入时仍返回合法 JSON，permissions.deny 为空数组（不是 null），便于 Claude
// CLI 识别为"未声明 deny"。
func BuildClaudeSettings(base Config, extra []string) ([]byte, error) {
	deny := make([]string, 0, len(base.Claude.DisabledTools)+len(base.Claude.DisabledSkills)+len(extra))
	deny = append(deny, base.Claude.DisabledTools...)
	for _, s := range base.Claude.DisabledSkills {
		if s == "" {
			continue
		}
		deny = append(deny, "Skill:"+s)
	}
	for _, s := range extra {
		if s == "" {
			continue
		}
		deny = append(deny, "Skill:"+s)
	}
	deny = dedupStrings(deny)
	if deny == nil {
		// 保证 JSON 渲染为 [] 而非 null
		deny = []string{}
	}

	settings := claudeSettings{
		Permissions: claudePermissions{Deny: deny},
	}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("nativefilter: marshal claude settings: %w", err)
	}
	return out, nil
}

func dedupStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
