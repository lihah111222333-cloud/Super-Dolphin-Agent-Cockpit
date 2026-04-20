package skill

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

const (
	defaultSkillCatalogCharBudget = 4096
	skillCatalogCoreLimit         = 8
	skillCatalogDetailLimit       = 96
)

type SkillCatalogProvider struct {
	skills     Service
	charBudget int
}

var _ contract.DynamicSectionProvider = (*SkillCatalogProvider)(nil)

func NewSkillCatalogProvider(svc Service) *SkillCatalogProvider {
	return &SkillCatalogProvider{skills: svc, charBudget: defaultSkillCatalogCharBudget}
}

func (p *SkillCatalogProvider) SectionName() string {
	return contract.DynamicSectionSkillCatalog
}

func (p *SkillCatalogProvider) Resolve(ctx context.Context, _ contract.SectionContext) (*string, error) {
	if p == nil || p.skills == nil {
		return nil, nil
	}
	infos, err := p.skills.ListSkills(ctx)
	if err != nil || len(infos) == 0 {
		return nil, nil
	}
	text := renderSkillCatalog(copySortedSkills(infos), p.effectiveBudget())
	if strings.TrimSpace(text) == "" {
		return nil, nil
	}
	return &text, nil
}

func (p *SkillCatalogProvider) effectiveBudget() int {
	if p == nil || p.charBudget <= 0 {
		return defaultSkillCatalogCharBudget
	}
	return p.charBudget
}

func renderSkillCatalog(infos []SkillInfo, budget int) string {
	if len(infos) == 0 {
		return ""
	}
	maxCore := min(len(infos), skillCatalogCoreLimit)
	for coreCount := maxCore; coreCount >= 0; coreCount-- {
		if text := buildCatalogSections(infos[:coreCount], infos[coreCount:], budget); text != "" {
			return text
		}
	}
	return ""
}

func buildCatalogSections(core, index []SkillInfo, budget int) string {
	blocks := make([]string, 0, 2)
	used := 0
	if coreBody := renderCoreLines(core); coreBody != "" {
		block := "## Core\n" + coreBody
		if len(block) > budget {
			return ""
		}
		blocks = append(blocks, block)
		used += len(block)
	}
	if len(index) > 0 {
		overhead := len("## Index\n")
		if len(blocks) > 0 {
			overhead += 2
		}
		indexBody := renderIndexLine(index, budget-used-overhead)
		if indexBody == "" {
			return ""
		}
		block := "## Index\n" + indexBody
		blocks = append(blocks, block)
	}
	if len(blocks) == 0 {
		return ""
	}
	text := strings.Join(blocks, "\n\n")
	if len(text) > budget {
		return ""
	}
	return text
}

func renderCoreLines(infos []SkillInfo) string {
	if len(infos) == 0 {
		return ""
	}
	lines := make([]string, 0, len(infos))
	for _, info := range infos {
		name := strings.TrimSpace(info.Name)
		if name == "" {
			continue
		}
		digest := skillCatalogDigest(info)
		if digest == "" {
			lines = append(lines, "- "+name)
			continue
		}
		lines = append(lines, "- "+name+": "+digest)
	}
	return strings.Join(lines, "\n")
}

func renderIndexLine(infos []SkillInfo, budget int) string {
	if budget <= 0 {
		return ""
	}
	names := collectSkillNames(infos)
	if len(names) == 0 {
		return ""
	}
	var b strings.Builder
	for i, name := range names {
		if appendIndexName(&b, name, len(names)-i-1, budget) {
			continue
		}
		break
	}
	return b.String()
}

func collectSkillNames(infos []SkillInfo) []string {
	names := make([]string, 0, len(infos))
	for _, info := range infos {
		name := strings.TrimSpace(info.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func appendIndexName(b *strings.Builder, name string, hidden, budget int) bool {
	part := indexNamePart(name, b.Len() > 0)
	if b.Len()+len(part)+overflowIndexSuffixLen(hidden) <= budget {
		b.WriteString(part)
		return true
	}
	if b.Len() == 0 {
		if len(name) > budget {
			b.WriteString(truncateRunes(name, budget))
			return false
		}
		b.WriteString(name)
	}
	appendIndexOverflow(b, hidden, budget)
	return false
}

func indexNamePart(name string, needsComma bool) string {
	if !needsComma {
		return name
	}
	return ", " + name
}

func appendIndexOverflow(b *strings.Builder, hidden, budget int) {
	if hidden <= 0 {
		return
	}
	suffix := fmt.Sprintf(", ... (+%d more)", hidden)
	if b.Len()+len(suffix) <= budget {
		b.WriteString(suffix)
	}
}

func overflowIndexSuffixLen(hidden int) int {
	if hidden <= 0 {
		return 0
	}
	return len(fmt.Sprintf(", ... (+%d more)", hidden))
}

func skillCatalogDigest(info SkillInfo) string {
	desc := strings.TrimSpace(info.Description)
	summary := strings.TrimSpace(info.Summary)
	switch {
	case desc == "":
		return truncateRunes(summary, skillCatalogDetailLimit)
	case summary == "" || strings.EqualFold(desc, summary):
		return truncateRunes(desc, skillCatalogDetailLimit)
	default:
		return truncateRunes(desc+" — "+summary, skillCatalogDetailLimit)
	}
}

func copySortedSkills(infos []SkillInfo) []SkillInfo {
	cloned := append([]SkillInfo(nil), infos...)
	sort.SliceStable(cloned, func(i, j int) bool {
		left := strings.ToLower(strings.TrimSpace(cloned[i].Name))
		right := strings.ToLower(strings.TrimSpace(cloned[j].Name))
		if left == right {
			return strings.TrimSpace(cloned[i].Name) < strings.TrimSpace(cloned[j].Name)
		}
		return left < right
	})
	return cloned
}
