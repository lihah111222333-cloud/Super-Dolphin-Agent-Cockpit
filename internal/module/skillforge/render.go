package skillforge

import (
	"fmt"
	"strings"
)

// summaryRunes は节摘要在 L1 渲染中的 rune 上限。
// 选 80 与 maxSectionTitleRunes 一致，便于在屏幕一行内展示。
const summaryRunes = 80

// RenderResult 是 forge 单 skill 转换后的内存产物，
// 由 atomic.go 落盘到 cache/<name>/。
type RenderResult struct {
	// SkillMD 是瘦身 SKILL.md 全文（含 frontmatter + 节索引）。
	SkillMD string
	// References 是 references/<filename>.md → 节正文。
	References map[string]string
}

// Render 把 ParsedSkill 渲染为瘦身 SKILL.md + 各节 references 文件。
// summaryOverride: anchor (H2 title) -> 手写摘要；nil 或缺失项使用自动抽取。
func Render(ps *ParsedSkill, summaryOverride map[string]string) (*RenderResult, error) {
	if ps == nil {
		return nil, fmt.Errorf("skillforge: Render(nil ParsedSkill)")
	}
	res := &RenderResult{References: map[string]string{}}

	var b strings.Builder
	b.WriteString("---\n")
	fmt.Fprintf(&b, "name: %s\n", ps.Name)
	fmt.Fprintf(&b, "description: %s\n", ps.Description)
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", ps.Name)

	if len(ps.Sections) == 0 {
		b.WriteString("（本 skill 无 H2 分节。）\n")
		res.SkillMD = b.String()
		return res, nil
	}

	b.WriteString("## 节索引（按需读，勿全文加载）\n\n")
	for i, sec := range ps.Sections {
		idx := i + 1
		fname := SectionFilename(idx, sec.Title)
		summary := summaryOverride[sec.Title]
		if summary == "" {
			summary = ExtractSummary(sec.Body, summaryRunes)
		}
		fmt.Fprintf(&b, "- %s — %s\n", sec.Title, summary)
		fmt.Fprintf(&b, "  详见 references/%s\n", fname)
		res.References[fname] = sec.Body + "\n"
	}
	b.WriteString("\n> 需要某节内容时，使用 Read 工具读取对应 references/ 文件，不要整文加载。\n")

	res.SkillMD = b.String()
	return res, nil
}
