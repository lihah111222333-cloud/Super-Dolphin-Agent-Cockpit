package codexapp

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

type turnStartParams struct {
	ThreadID             string          `json:"threadId"`
	Input                []turnInputItem `json:"input"`
	SelectedSkills       []string        `json:"selectedSkills,omitempty"`
	ManualSkillSelection bool            `json:"manualSkillSelection,omitempty"`
	Model                string          `json:"model,omitempty"`
	Effort               string          `json:"effort,omitempty"`
	OutputSchema         json.RawMessage `json:"outputSchema,omitempty"`
}

type turnInputItem struct {
	Type    string `json:"type"`
	Text    string `json:"text,omitempty"`
	URL     string `json:"url,omitempty"`
	Path    string `json:"path,omitempty"`
	Name    string `json:"name,omitempty"`
	Content string `json:"content,omitempty"`
}

type turnStartResult struct {
	Turn struct {
		ID string `json:"id"`
	} `json:"turn"`
}

func buildTurnStartParams(threadID string, req dto.TurnRequest) turnStartParams {
	selectedSkills := selectedSkillNames(req.Skills)
	inputs := turnInputsFromRequest(req.Inputs, req.Skills, req.TurnAssembly)
	return turnStartParams{
		ThreadID:             threadID,
		Input:                inputs,
		SelectedSkills:       selectedSkills,
		ManualSkillSelection: req.ManualSkillSelection,
		Model:                strings.TrimSpace(req.Overrides.Model),
		Effort:               strings.TrimSpace(req.Overrides.Effort),
		OutputSchema:         req.OutputSchema,
	}
}

func buildTurnSteerParams(threadID string, req dto.SteerRequest) map[string]any {
	params := map[string]any{
		"threadId":       threadID,
		"expectedTurnId": strings.TrimSpace(req.ExpectedTurnID),
		"input":          turnInputsFromRequest(req.Inputs, req.Skills, req.TurnAssembly),
	}
	if selectedSkills := selectedSkillNames(req.Skills); len(selectedSkills) > 0 {
		params["selectedSkills"] = selectedSkills
	}
	if req.ManualSkillSelection {
		params["manualSkillSelection"] = true
	}
	return params
}

func selectedSkillNames(skills []dto.SkillRef) []string {
	selected := make([]string, 0, len(skills))
	for _, skill := range skills {
		if name := strings.TrimSpace(skill.Name); name != "" {
			selected = append(selected, name)
		}
	}
	return selected
}

func turnInputsFromRequest(inputs []dto.InputItem, skills []dto.SkillRef, assembly dto.TurnAssembly) []turnInputItem {
	items := make([]turnInputItem, 0, len(inputs)+len(assembly.Attachments)+3)
	if skillPrompt, ok := buildSkillPromptInput(skills); ok {
		items = append(items, skillPrompt)
	}
	if userContext := assembly.RenderUserContextMessage(); userContext != "" {
		items = append(items, newTextTurnInput("text", userContext))
	}
	if systemContext := contract.FormatSystemContextBlock(assembly.SystemContext); systemContext != "" {
		items = append(items, newTextTurnInput("text", systemContext))
	}
	for _, attachment := range assembly.Attachments {
		if text := strings.TrimSpace(attachment.RenderText()); text != "" {
			items = append(items, newTextTurnInput("text", text))
		}
	}
	for _, item := range inputs {
		items = append(items, mapTurnInput(item))
	}
	return items
}

func mapTurnInput(item dto.InputItem) turnInputItem {
	switch strings.ToLower(strings.TrimSpace(item.Type)) {
	case "", "text":
		return textTurnInput(item)
	case "filecontent":
		return textTurnInput(dto.InputItem{Content: item.Content})
	case "image":
		return imageTurnInput(item)
	case "local_image", "localimage":
		return localImageTurnInput(item)
	case "file", "mention":
		return mentionTurnInput(item)
	default:
		return fallbackTurnInput(item)
	}
}

func textTurnInput(item dto.InputItem) turnInputItem {
	return newTextTurnInput("text", item.Content)
}

func imageTurnInput(item dto.InputItem) turnInputItem {
	if url := strings.TrimSpace(item.URL); url != "" {
		return turnInputItem{Type: "image", URL: url}
	}
	path := resolvedInputPath(item)
	if shared.IsRemoteTurnInput(path) {
		return turnInputItem{Type: "image", URL: path}
	}
	return turnInputItem{Type: "localImage", Path: path}
}

func localImageTurnInput(item dto.InputItem) turnInputItem {
	return turnInputItem{Type: "localImage", Path: resolvedInputPath(item)}
}

func mentionTurnInput(item dto.InputItem) turnInputItem {
	path := resolvedInputPath(item)
	name := strings.TrimSpace(item.Name)
	if name == "" {
		name = filepath.Base(path)
	}
	return turnInputItem{Type: "mention", Path: path, Name: name}
}

func fallbackTurnInput(item dto.InputItem) turnInputItem {
	return newTextTurnInput(item.Type, item.Content)
}

func resolvedInputPath(item dto.InputItem) string {
	if path := strings.TrimSpace(item.Path); path != "" {
		return path
	}
	return strings.TrimSpace(item.Content)
}
