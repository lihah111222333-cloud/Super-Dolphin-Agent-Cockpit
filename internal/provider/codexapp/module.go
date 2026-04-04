package codexapp

import (
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"go.uber.org/fx"
)

var Module = fx.Module("provider.codexapp",
	fx.Provide(
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
	),
	fx.Invoke(RegisterTranslators),
)

func buildSkillPromptInput(skills []dto.SkillRef) (turnInputItem, bool) {
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
	if len(sections) == 0 {
		return turnInputItem{}, false
	}
	text := strings.Join(sections, "\n\n")
	return newTextTurnInput("text", text), true
}

func resolveLocalTurnID(requested, fallback string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	return strings.TrimSpace(fallback)
}
