// Package nativefilter 把 spec §8 的 native CLI 过滤声明从"散在 skill meta 里的字段"
// 聚合成 Claude / Codex 子进程启动前可写入 settings 文件的扁平结构。
//
// 范围（P5 第一刀）：
//   - 纯聚合 + 去重 + 稳定排序，不做 IO、不接 fx、不写文件、不动 provider 启动路径
//   - spec §8.3 实测已落 appendix；deny 路径生效，allow 是 auto-approve
//
// 调用方负责选择数据源 + 把 SkillMeta-like 结构适配到 SkillSummary——本包不
// 直接 import skilllibrary，保持 leaf 依赖图干净。
//
// **当前消费方 scope**：claudecli driver.applyNativeFilter 只接 user-level
// skilllibrary.Store.List()，不含项目级 `<cwd>/.agent/skills` 的 skill。
// 这是有意决策（spec §8.3 appendix "Known scope limitation"），不是漏网。
// 如未来需要把项目 skill 纳入聚合，调用方注入 *skill.Service 并把
// SkillInfo 适配为 SkillSummary 即可，本聚合器无需改动。
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
//   - nil（JSON null 或缺省）→ 不施加 auto-approve 列表
//   - 非 nil（含空数组）     → 显式 auto-approve 列表，列入的工具调用跳过审批弹框
//
// 注意：实测（spec §8.3 appendix）证实 Claude CLI 的 permissions.allow 是
// auto-approve 而非严格白名单——未列入的工具仍可调用，只是会按默认审批策略
// 走流程。spec §3.1 旧措辞 "工具白名单" 不准，已在 appendix 校正
type ClaudeBase struct {
	DisabledSkills []string  `json:"disabled_skills,omitempty"`
	DisabledTools  []string  `json:"disabled_tools,omitempty"`
	AllowedTools   *[]string `json:"allowed_tools,omitempty"`
}

// CodexBase 对应 "codex" 段。spec §8.1 当前只列 disabled_tools。
//
// 实测警告（spec §8.3 appendix codex 段，2026-04-30）：
// codex-cli 0.121.0 没有声明式工具过滤机制——`config.toml [tools] disabled`
// 字段被 codex 静默忽略，--disabled-tools flag 不存在，无 "按 skill name 屏蔽
// native skill" 的能力。本类型字段保留作为数据形状占位，等未来 codex CLI
// 提供可声明的工具过滤机制后再激活；当前调用方不应依赖其消费效果。
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
	// Allow 写入 Claude CLI permissions.allow——auto-approve 列表，列入的
	// 工具调用跳过审批弹框；并非严格白名单（实测见 spec §8.3 appendix）。
	// nil = 未声明 auto-approve 列表；非 nil（含空切片）= 显式声明。
	Allow []string `json:"allow,omitempty"`
}

// CodexSettings 对应 codex 子进程启动前需要写入的过滤集。
//
// Deprecated（spec §8.3 appendix）：codex-cli 0.121.0 没有可消费此结构的目标
// 格式（disabled_tools 字段被 codex 静默忽略）。保留作为数据形状占位以备
// 未来 codex CLI 提供机制后激活；当前不应被任何 driver 接线消费。
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
// Allow 拼装规则（spec §3.1 + §8.1，语义见 §8.3 appendix）：
//  1. base.AllowedTools 为 nil 且无任何 active skill 声明 AllowedTools → Allow=nil
//     （表示"未声明 auto-approve 列表"，与 base 原 null 语义一致）。
//  2. 否则取并集（union）+ 去重 + 排序。多 skill 的语义是 OR——每个 skill 声明
//     "我会用这些工具，请跳过审批"，并集是 auto-approve 工具集。
//
// 历史背景：spec §3.1 把 allowed_tools 描述为"白名单"，但 §8.3 实测证实
// Claude CLI 的 permissions.allow 实际是 auto-approve 而非严格白名单。要严格
// 收敛工具集需走 --tools CLI flag，不在本聚合器范围。
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
//
// Deprecated（spec §8.3 appendix）：本函数输出当前在 codex-cli 0.121.0 上
// 没有可写入的目标格式。spec §8 codex 段标 deferred，等待 codex CLI 后续
// 版本提供声明式工具过滤机制（类似 Claude 的 permissions.deny 体系）。
// 函数实现 + 测试保留，便于机制就绪时一行接通；调用方当前不应消费返回值。
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

// aggregateAllowedTools 实现 auto-approve 列表拼装，返回 (allow, hasAllow)。
// hasAllow=false 表示"调用方应保留 Allow=nil"——既无 base 也无 skill 声明
// auto-approve 列表，与 "不写 permissions.allow 字段" 等价。
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
		// any=true 但没有任何非空 trim 后的工具名 → 显式 auto-approve 但为空集
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
