package skilllibrary

import "sort"

// AggregateAllReplacements 聚合所有 enabled skill 的 ReplacesNative 字段（遍历
// 所有 key："*"、"claude"、"codex" 等），去重排序返回被替代的原生工具名列表。
// 用于 prompt assembler 生成跨模型的工具抑制指令。
func AggregateAllReplacements(entries []SkillEntry) []string {
	seen := make(map[string]struct{})
	for _, e := range entries {
		if e.Meta == nil || e.Meta.Disabled {
			continue
		}
		for _, tools := range e.Meta.ReplacesNative {
			for _, name := range tools {
				if name != "" {
					seen[name] = struct{}{}
				}
			}
		}
	}
	return sortedKeys(seen)
}

func sortedKeys(seen map[string]struct{}) []string {
	out := make([]string, 0, len(seen))
	for n := range seen {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
