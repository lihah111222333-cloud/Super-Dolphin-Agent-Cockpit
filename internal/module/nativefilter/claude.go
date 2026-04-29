package nativefilter

import (
	"encoding/json"
	"fmt"
)

// claudeSettings 是 BuildClaudeSettings 的输出 JSON 结构。
// P5 只用 permissions.deny；§10 安全替代加上 permissions.allow allowlist，以严
// 格收敛会话可用工具集。allow 为空时不输出该字段（避免误读为 "什么
// 都不允许"）。
type claudeSettings struct {
	Permissions claudePermissions `json:"permissions"`
}

type claudePermissions struct {
	Deny  []string `json:"deny"`
	Allow []string `json:"allow,omitempty"`
}

// BuildClaudeSettings 把 base.Claude 的 disabled_tools + disabled_skills +
// denyExtra (ReplacesNative 聚合结果) 渲染为 permissions.deny；同时把
// base.Claude.AllowedTools + allowExtra (SkillMeta.AllowedTools 聚合结果) 渲染为
// permissions.allow allowlist。
//
// disabled_skills 与 denyExtra 都用 "Skill:<name>" 冒号格式 wrap（2026-04-30 实测
// 确认的正确语法；spec §8.3 文本里的 "Skill(name)" 圆括号实测不生效，待 errata）。
// disabled_tools / AllowedTools / allowExtra 直接使用字面。
// 输出各列表去重 + 稳定顺序。
//
// allowExtra 为空且 base.AllowedTools 也空 → 不输出 permissions.allow 字段
// （否则 Claude Code 会误读为 "什么工具都不允许"，session 塑灬）。
func BuildClaudeSettings(base Config, denyExtra, allowExtra []string) ([]byte, error) {
	deny := buildClaudeDenyList(base, denyExtra)
	allow := buildClaudeAllowList(base, allowExtra)

	perms := claudePermissions{Deny: deny}
	if len(allow) > 0 {
		perms.Allow = allow
	}
	settings := claudeSettings{Permissions: perms}
	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("nativefilter: marshal claude settings: %w", err)
	}
	return out, nil
}

func buildClaudeDenyList(base Config, denyExtra []string) []string {
	deny := make([]string, 0, len(base.Claude.DisabledTools)+len(base.Claude.DisabledSkills)+len(denyExtra))
	deny = append(deny, base.Claude.DisabledTools...)
	for _, s := range base.Claude.DisabledSkills {
		if s != "" {
			deny = append(deny, "Skill:"+s)
		}
	}
	for _, s := range denyExtra {
		if s != "" {
			deny = append(deny, "Skill:"+s)
		}
	}
	deny = dedupStrings(deny)
	if deny == nil {
		// 保证 JSON 渲染为 [] 而非 null
		deny = []string{}
	}
	return deny
}

func buildClaudeAllowList(base Config, allowExtra []string) []string {
	allow := make([]string, 0, len(base.Claude.AllowedTools)+len(allowExtra))
	allow = append(allow, base.Claude.AllowedTools...)
	allow = append(allow, allowExtra...)
	return dedupStrings(allow)
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
