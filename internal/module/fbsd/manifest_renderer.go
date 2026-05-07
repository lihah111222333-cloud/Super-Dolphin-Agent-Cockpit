package fbsd

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dtoskill "github.com/anthropic-ai/super-agent-v3/internal/dto/skill"
)

const defaultManifestBudget = 8192

const skillManifestHeader = "## 可用 skills（按需读，勿全文加载）\n\n调用 skill_read_section(name, anchor) 读某节正文。\n\n"

const skillManifestTruncatedFooter = "\n（更多 skill 因 budget 截断省略）\n"

// ManifestRenderer implements contract.SkillManifestRenderer by combining
// the skill library store with FBSD tier-aware rendering.
type ManifestRenderer struct {
	entries      contract.SkillManifestEntryLister
	descriptions contract.SkillDescriptionParser
	tracker      *Tracker
}

// NewManifestRenderer creates a ManifestRenderer. Either or both params may
// be nil; RenderSkillManifest returns "" in that case.
func NewManifestRenderer(entries contract.SkillManifestEntryLister, descriptions contract.SkillDescriptionParser, tracker *Tracker) *ManifestRenderer {
	return &ManifestRenderer{entries: entries, descriptions: descriptions, tracker: tracker}
}

// RenderSkillManifest satisfies contract.SkillManifestRenderer.
func (r *ManifestRenderer) RenderSkillManifest() string {
	if r == nil || r.entries == nil {
		return ""
	}
	entries, err := r.entries.List()
	if err != nil || len(entries) == 0 {
		return ""
	}
	if r.tracker != nil && r.tracker.Enabled() {
		return buildManifestFBSD(entries, r.descriptions, r.tracker, EnvTierConfig(), time.Now())
	}
	return buildManifest(entries, r.descriptions, defaultManifestBudget)
}

// buildManifest renders L1-C full-detail manifest (no FBSD tier degradation).
func buildManifest(entries []dtoskill.SkillEntry, descriptions contract.SkillDescriptionParser, budgetChars int) string {
	if len(entries) == 0 {
		return ""
	}
	sorted := make([]dtoskill.SkillEntry, len(entries))
	copy(sorted, entries)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Meta.Name < sorted[j].Meta.Name })

	var b strings.Builder
	b.WriteString(skillManifestHeader)

	truncated := false
	for _, e := range sorted {
		block := renderL1CBlock(e, descriptions)
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

func renderL1CBlock(e dtoskill.SkillEntry, descriptions contract.SkillDescriptionParser) string {
	desc := descriptionOf(e, descriptions)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s — %s\n", e.Meta.Name, desc))
	if len(e.Meta.SectionSummaries) > 0 {
		b.WriteString("  节索引：\n")
		anchors := sortedStringKeys(e.Meta.SectionSummaries)
		for _, a := range anchors {
			b.WriteString(fmt.Sprintf("    - %s — %s\n", a, e.Meta.SectionSummaries[a]))
		}
	}
	return b.String()
}

func descriptionOf(e dtoskill.SkillEntry, descriptions contract.SkillDescriptionParser) string {
	if descriptions == nil {
		return ""
	}
	return descriptions.Description(e.SkillMD)
}

func renderWarmBlock(e dtoskill.SkillEntry, descriptions contract.SkillDescriptionParser) string {
	desc := descriptionOf(e, descriptions)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("- %s — %s\n", e.Meta.Name, desc))
	if len(e.Meta.SectionSummaries) > 0 {
		anchors := sortedStringKeys(e.Meta.SectionSummaries)
		b.WriteString("  节: [" + strings.Join(anchors, ", ") + "]\n")
	}
	return b.String()
}

func renderColdBlock(e dtoskill.SkillEntry, descriptions contract.SkillDescriptionParser) string {
	return fmt.Sprintf("- %s: %s\n", e.Meta.Name, descriptionOf(e, descriptions))
}

func buildManifestFBSD(entries []dtoskill.SkillEntry, descriptions contract.SkillDescriptionParser, tracker *Tracker, cfg TierConfig, now time.Time) string {
	if len(entries) == 0 || tracker == nil {
		return ""
	}
	wsStats, glStats := tracker.Snapshot()
	assignments := AssignTiers(entries, wsStats, glStats, cfg, now)
	var b strings.Builder
	b.WriteString(skillManifestHeader)
	for _, a := range assignments {
		switch a.Tier {
		case TierHot:
			b.WriteString(renderL1CBlock(a.Skill, descriptions))
		case TierWarm:
			b.WriteString(renderWarmBlock(a.Skill, descriptions))
		case TierCold:
			b.WriteString(renderColdBlock(a.Skill, descriptions))
			// TierFrozen: omitted
		}
	}
	return b.String()
}

func sortedStringKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
