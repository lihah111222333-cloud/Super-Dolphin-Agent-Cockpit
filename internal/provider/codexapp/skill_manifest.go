package codexapp

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/skilllibrary"
)

const skillManifestHeader = `## 可用 skills（按需读，勿全文加载）

调用 skill_read_section(name, anchor) 读某节正文。

`

const skillManifestTruncatedFooter = "\n（更多 skill 因 budget 截断省略）\n"

// buildSkillManifest 渲染 Codex base instructions 中的 skill 列表（L1-C 形态）：
// 每个 skill 输出 name + description + 节索引（标题 + 摘要）。
// 总长度超过 budgetChars 时按 skill 边界截尾，附加省略提示。
//
// FBSD 频次降级（spec §9）暂未实现；本期所有 skill 一律 L1-C。
func buildSkillManifest(entries []skilllibrary.SkillEntry, budgetChars int) string {
	if len(entries) == 0 {
		return ""
	}
	sorted := make([]skilllibrary.SkillEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Meta.Name < sorted[j].Meta.Name })

	var b strings.Builder
	b.WriteString(skillManifestHeader)

	truncated := false
	for _, e := range sorted {
		block := renderL1CBlock(e)
		if b.Len()+len(block) > budgetChars {
			truncated = true
			break
		}
		b.WriteString(block)
	}
	if truncated {
		b.WriteString(skillManifestTruncatedFooter)
	}
	return b.String()
}

func renderL1CBlock(e skilllibrary.SkillEntry) string {
	desc := extractDescriptionFromSkillMD(e.SkillMD)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s — %s\n", e.Meta.Name, desc))
	if len(e.Meta.SectionSummaries) > 0 {
		b.WriteString("  节索引：\n")
		anchors := sortedKeys(e.Meta.SectionSummaries)
		for _, a := range anchors {
			b.WriteString(fmt.Sprintf("    - %s — %s\n", a, e.Meta.SectionSummaries[a]))
		}
	}
	return b.String()
}

// extractDescriptionFromSkillMD 从瘦身 SKILL.md frontmatter 里提取 description。
// 简化实现：前 10 行内查找 "description:"。
func extractDescriptionFromSkillMD(src string) string {
	for i, ln := range strings.Split(src, "\n") {
		if i > 10 {
			break
		}
		if strings.HasPrefix(ln, "description:") {
			return strings.TrimSpace(strings.TrimPrefix(ln, "description:"))
		}
	}
	return ""
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
