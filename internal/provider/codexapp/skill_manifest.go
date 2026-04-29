package codexapp

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/module/fbsd"
	"github.com/anthropic-ai/super-agent-v3/internal/module/skillforge"
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
	desc := descriptionOf(e)
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

// descriptionOf 从 SKILL.md frontmatter 提取 description，失败返回空串。
func descriptionOf(e skilllibrary.SkillEntry) string {
	ps, err := skillforge.Parse(e.SkillMD)
	if err != nil {
		return ""
	}
	return ps.Description
}

// renderWarmBlock 是 L1-B 渲染：name + desc + 节标题列表（无摘要）。适用于
// FBSD Warm tier。
func renderWarmBlock(e skilllibrary.SkillEntry) string {
	desc := descriptionOf(e)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s — %s\n", e.Meta.Name, desc))
	if len(e.Meta.SectionSummaries) > 0 {
		anchors := sortedKeys(e.Meta.SectionSummaries)
		b.WriteString("  节: [" + strings.Join(anchors, ", ") + "]\n")
	}
	return b.String()
}

// renderColdBlock 是 L1-A 渲染：仅 name + desc。适用于 FBSD Cold tier。
func renderColdBlock(e skilllibrary.SkillEntry) string {
	return fmt.Sprintf("- %s: %s\n", e.Meta.Name, descriptionOf(e))
}

// buildSkillManifestFBSD 是开启 SUPER_DOLPHIN_SKILL_FBSD 后的渲染入口：
// AssignTiers 后按 Hot/Warm/Cold/Frozen 分块输出，Frozen tier 不出现于 manifest。
// tracker / entries 为空 → 返回空字符串（调用方负责 fallback 走 buildSkillManifest）。
func buildSkillManifestFBSD(entries []skilllibrary.SkillEntry, tracker *fbsd.Tracker, cfg fbsd.TierConfig, now time.Time) string {
	if len(entries) == 0 || tracker == nil {
		return ""
	}
	wsStats, glStats := tracker.Snapshot()
	assignments := fbsd.AssignTiers(entries, wsStats, glStats, cfg, now)
	var b strings.Builder
	b.WriteString(skillManifestHeader)
	for _, a := range assignments {
		switch a.Tier {
		case fbsd.TierHot:
			b.WriteString(renderL1CBlock(a.Skill))
		case fbsd.TierWarm:
			b.WriteString(renderWarmBlock(a.Skill))
		case fbsd.TierCold:
			b.WriteString(renderColdBlock(a.Skill))
			// TierFrozen: 不输出任何内容
		}
	}
	return b.String()
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// renderSkillManifest 在 driver 维度选择走 FBSD tier 渲染还是 P3 单 tier。
// SUPER_DOLPHIN_SKILL_FBSD=on 且 tracker 注入成功时走 buildSkillManifestFBSD；
// 其他情况向后兼容到 buildSkillManifest（spec §12 P6 灰度规范）。
func (d *driver) renderSkillManifest(entries []skilllibrary.SkillEntry) string {
	if d != nil && d.tracker != nil && d.tracker.Enabled() {
		return buildSkillManifestFBSD(entries, d.tracker, fbsd.EnvTierConfig(), time.Now())
	}
	return buildSkillManifest(entries, defaultManifestBudget)
}
