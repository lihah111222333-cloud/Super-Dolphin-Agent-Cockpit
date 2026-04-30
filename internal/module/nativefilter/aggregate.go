// Package nativefilter 把 spec §8 的 native CLI 过滤声明从"散在 skill meta 里的字段"
// 聚合成 Claude / Codex 子进程启动前可写入 settings 文件的扁平结构。
//
// 范围（P5 第一刀）：
//   - 纯聚合 + 去重 + 稳定排序，不做 IO、不接 fx、不写文件、不动 provider 启动路径
//   - 不假设 spec §8.3 验证已完成；输出的 deny / allow 字段直接对齐 spec §8.2
//     的候选机制 (`permissions.deny: ["Skill(name)", "Read", ...]`)。真实 CLI 行为
//     由后续接线 phase 实测后再决定是否调整字段名 / wrapper 形态。
//
// 调用方负责把 internal/module/skilllibrary.SkillMeta 适配到 SkillSummary——
// 本包不直接 import skilllibrary，保持 leaf 依赖图干净。
package nativefilter

import (
	"sort"
	"strings"
)

// BaseConfig 对应 ~/.multi-agent/native-cli-filter.json 的解码结果。
type BaseConfig struct {
	Claude ClaudeBase `json:"claude"`
	Codex  CodexBase  `json:"codex"`
}

// ClaudeBase 对应 native-cli-filter.json 的 "claude" 段。
//
// AllowedTools 用 *[]string 区分两种语义：
//   - nil（JSON null 或缺省）→ 不施加 allowlist
//   - 非 nil（含空数组）     → 显式 allowlist，仅这些工具被放行
type ClaudeBase struct {
	DisabledSkills []string  `json:"disabled_skills,omitempty"`
	DisabledTools  []string  `json:"disabled_tools,omitempty"`
	AllowedTools   *[]string `json:"allowed_tools,omitempty"`
}

// CodexBase 对应 "codex" 段。spec §8.1 当前只列 disabled_tools。
type CodexBase struct {
	DisabledTools []string `json:"disabled_tools,omitempty"`
}

// SkillSummary 是 nativefilter 关心的 skill 字段子集，避免反向依赖 skilllibrary。
type SkillSummary struct {
	Name           string
	Disabled       bool
	AllowedTools   []string
	ReplacesNative map[string][]string
}

// ClaudeSettings 对应 <workspace>/.claude/settings.json 的子集。
type ClaudeSettings struct {
	Permissions ClaudePermissions `json:"permissions"`
}

type ClaudePermissions struct {
	// Deny 包含 base.disabled_tools 原始名 + base.disabled_skills 与
	// skill.replaces_native.claude 经 Skill(name) wrap 后的名字。
	Deny []string `json:"deny,omitempty"`
	// Allow nil 表示不施加 allowlist；非 nil 时为合并后的工具白名单。
	Allow []string `json:"allow,omitempty"`
}

// CodexSettings 对应 codex 子进程启动前需要写入的过滤集。
type CodexSettings struct {
	DisabledTools []string `json:"disabled_tools,omitempty"`
}

// AggregateClaude 把 base + active skills 聚合成 ClaudeSettings。
//
// Deny 拼装规则（spec §8.2）：
//  1. base.DisabledTools 原样进 Deny（裸名，例如 "Read"）。
//  2. base.DisabledSkills + skill.ReplacesNative["claude"] 合并去重，
//     再统一 wrap 成 "Skill(name)" 形式进 Deny。
//
// Allow 拼装规则（spec §3.1 + §8.1）：
//  1. base.AllowedTools 为 nil 且无任何 active skill 声明 AllowedTools → Allow=nil
//     （表示"不施加 allowlist"，与 base 原 null 语义一致）。
//  2. 否则取并集（union）+ 去重 + 排序。多 skill 的语义是 OR——每个 skill 声明
//     "我至少需要这些工具"，并集是允许工具的最小覆盖集。
//
// Disabled=true 的 skill 一律忽略，不参与 ReplacesNative / AllowedTools 聚合。
// 输出顺序稳定排序，便于 settings.json 内容稳定（避免空 diff）。
func AggregateClaude(base ClaudeBase, skills []SkillSummary) ClaudeSettings {
	denySet := newStringSet()
	for _, t := range base.DisabledTools {
		denySet.addTrim(t)
	}
	skillDenySet := newStringSet()
	for _, s := range base.DisabledSkills {
		skillDenySet.addTrim(s)
	}
	for _, sk := range skills {
		if sk.Disabled {
			continue
		}
		for _, raw := range sk.ReplacesNative["claude"] {
			skillDenySet.addTrim(raw)
		}
	}
	for _, name := range skillDenySet.sorted() {
		denySet.add("Skill(" + name + ")")
	}

	allow, hasAllow := aggregateAllowedTools(base.AllowedTools, skills)
	settings := ClaudeSettings{
		Permissions: ClaudePermissions{Deny: denySet.sorted()},
	}
	if hasAllow {
		settings.Permissions.Allow = allow
	}
	return settings
}

// AggregateCodex 聚合 codex 端的 disabled_tools。
// base.DisabledTools + 每条 active skill 的 ReplacesNative["codex"] 合并去重排序。
func AggregateCodex(base CodexBase, skills []SkillSummary) CodexSettings {
	disabled := newStringSet()
	for _, t := range base.DisabledTools {
		disabled.addTrim(t)
	}
	for _, sk := range skills {
		if sk.Disabled {
			continue
		}
		for _, raw := range sk.ReplacesNative["codex"] {
			disabled.addTrim(raw)
		}
	}
	return CodexSettings{DisabledTools: disabled.sorted()}
}

// aggregateAllowedTools 实现 Allow 拼装规则，返回 (allow, hasAllow)。
// hasAllow=false 表示"调用方应保留 Allow=nil"。
func aggregateAllowedTools(base *[]string, skills []SkillSummary) ([]string, bool) {
	any := false
	set := newStringSet()
	if base != nil {
		any = true
		for _, t := range *base {
			set.addTrim(t)
		}
	}
	for _, sk := range skills {
		if sk.Disabled || len(sk.AllowedTools) == 0 {
			continue
		}
		any = true
		for _, t := range sk.AllowedTools {
			set.addTrim(t)
		}
	}
	if !any {
		return nil, false
	}
	out := set.sorted()
	if out == nil {
		// any=true 但没有任何非空 trim 后的工具名 → 显式 allowlist 但为空集
		// （base.allowed_tools=[] 或全空白），调用方应当区分于 nil。
		return []string{}, true
	}
	return out, true
}

// stringSet 提供 trim + 去重 + 排序输出的最小集合工具。
type stringSet map[string]struct{}

func newStringSet() stringSet { return stringSet{} }

func (s stringSet) addTrim(v string) {
	v = strings.TrimSpace(v)
	if v == "" {
		return
	}
	s[v] = struct{}{}
}

func (s stringSet) add(v string) {
	if v == "" {
		return
	}
	s[v] = struct{}{}
}

func (s stringSet) sorted() []string {
	if len(s) == 0 {
		return nil
	}
	out := make([]string, 0, len(s))
	for k := range s {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
