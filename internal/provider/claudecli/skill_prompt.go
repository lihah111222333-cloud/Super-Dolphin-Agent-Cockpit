package claudecli

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

func buildSkillSection(skills []dto.SkillRef) string {
	sections := make([]string, 0, 2)
	if list := buildSkillList(skills); list != "" {
		sections = append(sections, list)
	}
	if prompt := buildSkillPromptText(skills); prompt != "" {
		sections = append(sections, prompt)
	}
	return strings.Join(sections, "\n\n")
}

func buildSkillList(skills []dto.SkillRef) string {
	lines := []string{"skills:"}
	for _, skill := range skills {
		if name := strings.TrimSpace(skill.Name); name != "" {
			lines = append(lines, "- "+name)
		}
	}
	if len(lines) == 1 {
		return ""
	}
	return strings.Join(lines, "\n")
}

func buildSkillPromptText(skills []dto.SkillRef) string {
	sections := make([]string, 0, len(skills))
	for _, skill := range skills {
		section := strings.TrimSpace(skill.Prompt)
		if section == "" {
			continue
		}
		if name := strings.TrimSpace(skill.Name); name != "" {
			section = "[skill:" + name + "]\n" + section
		}
		sections = append(sections, section)
	}
	return strings.Join(sections, "\n\n")
}
