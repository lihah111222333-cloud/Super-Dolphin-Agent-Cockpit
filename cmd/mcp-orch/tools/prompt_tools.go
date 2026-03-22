package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	promptstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/prompt"
)

type promptListInput struct {
	Keyword string `json:"keyword,omitempty"`
}

type promptGetInput struct {
	PromptKey string `json:"prompt_key"`
}

type promptTemplateDTO struct {
	ID          int64           `json:"id"`
	PromptKey   string          `json:"prompt_key"`
	Title       string          `json:"title"`
	AgentKey    string          `json:"agent_key"`
	ToolName    string          `json:"tool_name"`
	PromptText  string          `json:"prompt_text"`
	Variables   json.RawMessage `json:"variables,omitempty"`
	Tags        json.RawMessage `json:"tags,omitempty"`
	Enabled     bool            `json:"enabled"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Description string          `json:"description"`
}

func HandlePromptList(store promptstore.Store) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in promptListInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return listPromptTemplates(ctx, store, in)
	}
}

func HandlePromptGet(store promptstore.Store) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in promptGetInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return getPromptTemplate(ctx, store, in)
	}
}

func promptToolDefinitions(store promptstore.Store) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "prompt_list",
			Description: "List available prompt templates. Templates can be used to generate structured prompts for agents.",
			InputSchema: ObjectSchema(map[string]Schema{
				"keyword": StringSchema("Search keyword (optional)."),
			}),
			Handler: HandlePromptList(store),
		},
		{
			Name:        "prompt_get",
			Description: "Get a specific prompt template by its key, including the prompt text and variables.",
			InputSchema: ObjectSchema(map[string]Schema{
				"prompt_key": StringSchema("Prompt template key."),
			}, "prompt_key"),
			Handler: HandlePromptGet(store),
		},
	}
}

func listPromptTemplates(ctx context.Context, store promptstore.Store, input promptListInput) ([]promptTemplateDTO, error) {
	if store == nil {
		return nil, errors.New("prompt store is not configured")
	}
	templates, err := store.List(ctx, promptstore.ListFilter{
		AgentKey: "",
		Keyword:  strings.TrimSpace(input.Keyword),
		Limit:    resourceListLimit,
	})
	if err != nil {
		return nil, err
	}
	return mapPromptTemplates(templates), nil
}

func getPromptTemplate(ctx context.Context, store promptstore.Store, input promptGetInput) (promptTemplateDTO, error) {
	if store == nil {
		return promptTemplateDTO{}, errors.New("prompt store is not configured")
	}
	promptKey, err := requireTrimmed(input.PromptKey, "prompt_key")
	if err != nil {
		return promptTemplateDTO{}, err
	}
	template, err := store.Get(ctx, promptKey)
	if err != nil {
		return promptTemplateDTO{}, err
	}
	if template == nil {
		return promptTemplateDTO{}, errors.New("prompt template not found")
	}
	return promptTemplateFromStore(*template), nil
}

func mapPromptTemplates(templates []promptstore.PromptTemplate) []promptTemplateDTO {
	mapped := make([]promptTemplateDTO, 0, len(templates))
	for _, template := range templates {
		mapped = append(mapped, promptTemplateFromStore(template))
	}
	return mapped
}

func promptTemplateFromStore(template promptstore.PromptTemplate) promptTemplateDTO {
	return promptTemplateDTO{
		ID:          template.ID,
		PromptKey:   template.PromptKey,
		Title:       template.Title,
		AgentKey:    template.AgentKey,
		ToolName:    template.ToolName,
		PromptText:  template.PromptText,
		Variables:   cloneRawMessage(template.Variables),
		Tags:        cloneRawMessage(template.Tags),
		Enabled:     template.Enabled,
		CreatedBy:   template.CreatedBy,
		UpdatedBy:   template.UpdatedBy,
		CreatedAt:   template.CreatedAt,
		UpdatedAt:   template.UpdatedAt,
		Description: template.Description,
	}
}
