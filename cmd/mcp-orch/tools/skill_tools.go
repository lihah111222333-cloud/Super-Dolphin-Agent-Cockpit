package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"

	skillmodule "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
)

const skillExpandDefaultMaxBytes int64 = 20000

type skillListInput struct {
	Keyword string `json:"keyword,omitempty"`
}

type skillExpandInput struct {
	Name     string `json:"name"`
	Section  string `json:"section,omitempty"`
	MaxBytes *int64 `json:"max_bytes,omitempty"`
}

type skillListDTO struct {
	Name                   string                 `json:"name"`
	Summary                string                 `json:"summary"`
	Description            string                 `json:"description"`
	Trust                  skillmodule.TrustScope `json:"trust"`
	ContentHash            string                 `json:"content_hash"`
	DisableModelInvocation bool                   `json:"disable_model_invocation"`
}

type toolTextContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type toolErrorResult struct {
	Content []toolTextContent `json:"content"`
	IsError bool              `json:"isError"`
}

func HandleSkillList(svc skillmodule.Service) ToolHandler {
	return makeHandler(svc, "skill service", func(ctx context.Context, in skillListInput) ([]skillListDTO, error) {
		return listSkills(ctx, svc, in)
	})
}

func HandleSkillExpand(svc skillmodule.Service) ToolHandler {
	return makeHandler(svc, "skill service", func(ctx context.Context, in skillExpandInput) (any, error) {
		return expandSkill(ctx, svc, in)
	})
}

func skillToolDefinitions(svc skillmodule.Service) []ToolDefinition {
	return buildToolDefinitions(
		defineTool(
			"skill_list",
			"List available skills that can be expanded on demand. Returns metadata only and intentionally omits internal fields such as install directories and trigger words.",
			ObjectSchema(map[string]Schema{
				"keyword": StringSchema("Search keyword (optional). Filters by skill name, summary, and description."),
			}),
			HandleSkillList(svc),
		),
		defineTool("skill_expand", skillExpandDescription(), skillExpandSchema(), HandleSkillExpand(svc)),
	)
}

func skillExpandDescription() string {
	return fmt.Sprintf("Read a known skill only when you need its full instructions or a specific resource file after using skill_list; do not call this for discovery when the skill name is still unknown or when the metadata from skill_list is already enough. section semantics: empty returns the full SKILL.md body, a value starting with # selects a Markdown H2/H3 subsection, and a relative path reads a file inside the skill directory such as references/api.md or scripts/setup.sh. max_bytes defaults to %d when omitted; if the selected content is larger, content is truncated and truncated=true while total_bytes reports the original size. If the skill is not found, the tool returns an error result that best-effort lists available skill names.", skillExpandDefaultMaxBytes)
}

func skillExpandSchema() Schema {
	maxBytes := IntegerSchema(fmt.Sprintf("Maximum bytes to return. Defaults to %d when omitted.", skillExpandDefaultMaxBytes))
	maxBytes["minimum"] = 1
	return ObjectSchema(map[string]Schema{
		"name":      StringSchema("Skill name."),
		"section":   StringSchema("Optional selector: empty means the full SKILL.md body, a value starting with # selects a Markdown H2/H3 subsection, and a relative path reads a resource file inside the skill directory."),
		"max_bytes": maxBytes,
	}, "name")
}

func listSkills(ctx context.Context, svc skillmodule.Service, input skillListInput) ([]skillListDTO, error) {
	if err := requireDependency(svc, "skill service"); err != nil {
		return nil, err
	}
	skills, err := svc.ListSkills(ctx)
	if err != nil {
		return nil, err
	}
	keyword := strings.ToLower(strings.TrimSpace(input.Keyword))
	mapped := make([]skillListDTO, 0, len(skills))
	for _, current := range skills {
		if keyword != "" && !skillMatchesKeyword(current, keyword) {
			continue
		}
		mapped = append(mapped, skillListDTO{
			Name:                   current.Name,
			Summary:                current.Summary,
			Description:            current.Description,
			Trust:                  current.Trust,
			ContentHash:            current.ContentHash,
			DisableModelInvocation: current.DisableModelInvocation,
		})
	}
	return mapped, nil
}

func expandSkill(ctx context.Context, svc skillmodule.Service, input skillExpandInput) (any, error) {
	if err := requireDependency(svc, "skill service"); err != nil {
		return nil, err
	}
	result, err := svc.Expand(ctx, skillmodule.SkillExpandParams{
		Name:     input.Name,
		Section:  input.Section,
		MaxBytes: input.MaxBytes,
	})
	if err == nil {
		return result, nil
	}
	if skillmodule.IsExpandInvalidParams(err) {
		return newToolErrorResult("invalid params: " + err.Error()), nil
	}
	if skillmodule.IsExpandNotFound(err) {
		return newToolErrorResult(skillNotFoundMessage(ctx, svc, input.Name)), nil
	}
	return nil, err
}

func skillMatchesKeyword(info skillmodule.SkillInfo, keyword string) bool {
	haystack := strings.ToLower(strings.Join([]string{info.Name, info.Summary, info.Description}, "\n"))
	return strings.Contains(haystack, keyword)
}

func skillNotFoundMessage(ctx context.Context, svc skillmodule.Service, name string) string {
	message := fmt.Sprintf("skill %q not found", strings.TrimSpace(name))
	available := availableSkillNames(ctx, svc)
	if len(available) == 0 {
		return message
	}
	return message + ". Available skills: " + strings.Join(available, ", ")
}

func availableSkillNames(ctx context.Context, svc skillmodule.Service) []string {
	skills, err := svc.ListSkills(ctx)
	if err != nil {
		return nil
	}
	names := make([]string, 0, len(skills))
	for _, current := range skills {
		if name := strings.TrimSpace(current.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func newToolErrorResult(text string) toolErrorResult {
	return toolErrorResult{
		Content: []toolTextContent{{Type: "text", Text: text}},
		IsError: true,
	}
}
