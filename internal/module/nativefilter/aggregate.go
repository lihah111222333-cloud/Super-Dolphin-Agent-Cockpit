package nativefilter

import (
	"sort"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

// AggregateReplacesNative 把所有 enabled skill 的 ReplacesNative[cli] 字段
// 聚合 + dedup + sort，返回稳定顺序。spec §8.2 规定该聚合是声明式叠加层：
// 每条 active skill 用自己的 sidecar 声明它"替代"了哪些 native cli skill / tool，
// nativefilter 把这些声明并到全局基线 disabled 列表上。
//
// AggregateAllowedTools 同语义但走 SkillMeta.AllowedTools 字段，面向 spec
// §10 要求的全局 allowlist enforce。
//
// 跳过的条目：
//   - Meta == nil（防御性，正常 SkillEntry 不会出现）
//   - Meta.Disabled = true（与 cache reconcile 的"disabled 不进 cache"语义一致）
//   - ReplacesNative[cli] 中的空字符串（防御性）
//
// cli 取值通常为 "claude" / "codex"；未知 key 返回空 slice 不报错。
func AggregateReplacesNative(entries []skilllibrary.SkillEntry, cli string) []string {
	seen := make(map[string]struct{})
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		for _, name := range e.Meta.ReplacesNative[cli] {
			if name == "" {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	return sortedSeen(seen)
}

// AggregateAllowedTools 聚合所有 enabled skill 的 SkillMeta.AllowedTools 并集，
// 返回稳定排序。该集合与全局 base.Claude.AllowedTools 合并后作为 Claude
// settings.json `permissions.allow` 列表（spec §10 要求运行时收敛）。
//
// 设计折中：Claude Code 2.1.119 的 permissions.allow 是 session 级 allowlist，
// 不支持 per-skill 精确控制；本实现是全局并集近似，是在现有 CLI 能力下
// 能做到的最接近 spec 语义的严格收敛。
func AggregateAllowedTools(entries []skilllibrary.SkillEntry) []string {
	seen := make(map[string]struct{})
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		for _, name := range e.Meta.AllowedTools {
			if name == "" {
				continue
			}
			seen[name] = struct{}{}
		}
	}
	return sortedSeen(seen)
}

func sortedSeen(seen map[string]struct{}) []string {
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
