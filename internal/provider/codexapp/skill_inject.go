package codexapp

import (
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

var (
	_ contract.SkillInjectionPort  = codexSkillInjectionPort{}
	_ contract.NativeSkillDetector = codexSkillInjectionPort{}
)

const defaultSkillManifestTokenBudget = 3000

type codexSkillInjectionPort struct{}

func NewSkillInjectionPort() codexSkillInjectionPort {
	return codexSkillInjectionPort{}
}

func (codexSkillInjectionPort) InjectL1Manifest(baseInstructions, manifest string) string {
	baseInstructions = strings.TrimSpace(baseInstructions)
	manifest = strings.TrimSpace(manifest)
	switch {
	case baseInstructions == "":
		return manifest
	case manifest == "":
		return baseInstructions
	default:
		return baseInstructions + "\n\n" + manifest
	}
}

func (codexSkillInjectionPort) BuildTurnSection(refs []dto.SkillRef) (string, bool) {
	item, ok := buildSkillPromptInput(refs)
	if !ok {
		return "", false
	}
	return strings.TrimSpace(item.Text), true
}

func (codexSkillInjectionPort) DetectNativeSkills(_ string) []string {
	return nil
}

func (codexSkillInjectionPort) ReservedTokens() int {
	return defaultSkillManifestTokenBudget
}

func buildSkillPromptInput(skills []dto.SkillRef) (turnInputItem, bool) {
	parts := make([]string, 0, 2)
	if list := buildSkillNameList(skills); list != "" {
		parts = append(parts, list)
	}
	if blocks := buildSkillBlocks(skills); len(blocks) > 0 {
		parts = append(parts, strings.Join(blocks, "\n\n"))
	}
	if len(parts) == 0 {
		return turnInputItem{}, false
	}
	return newTextTurnInput("text", strings.Join(parts, "\n\n")), true
}

func buildSkillNameList(skills []dto.SkillRef) string {
	lines := []string{"skills:"}
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" || skill.Mode.Effective() == dto.SkillModeNone {
			continue
		}
		lines = append(lines, "- "+name)
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func buildSkillBlocks(skills []dto.SkillRef) []string {
	blocks := make([]string, 0, len(skills))
	for _, skill := range skills {
		switch skill.Mode.Effective() {
		case dto.SkillModeFull:
			if block, ok := skillpkg.RenderSkillBlock(skill.Name, skill.Prompt, skill.Summary, string(skill.Mode)); ok {
				blocks = append(blocks, block)
			}
		case dto.SkillModeSummary:
			if block, ok := renderLegacySummarySkillBlock(skill.Name, skill.Summary); ok {
				blocks = append(blocks, block)
			}
		}
	}
	return blocks
}

func renderLegacySummarySkillBlock(name, summary string) (string, bool) {
	name = strings.TrimSpace(name)
	summary = strings.TrimSpace(summary)
	if _, ok := skillpkg.RenderSkillBlock(name, "", summary, string(dto.SkillModeSummary)); !ok {
		return "", false
	}
	return fmt.Sprintf("[skill:%s]\n摘要: %s\n使用方式: Call skill_expand_body(%q) for full body", name, summary, name), true
}
