package dashboard

import (
	"context"
	"time"

	"github.com/creachadair/jrpc2/handler"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type promptListParams struct {
	Cwd string `json:"cwd,omitempty"`
}

type promptWriteParams struct {
	ID          string `json:"id,omitempty"`
	Name        string `json:"name"`
	Content     string `json:"content,omitempty"`
	Description string `json:"description,omitempty"`
	AgentType   string `json:"agentType,omitempty"`
	Cwd         string `json:"cwd,omitempty"`
}

type promptDeleteParams struct {
	ID  string `json:"id"`
	Cwd string `json:"cwd,omitempty"`
}

type promptRPCItem struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Content     string    `json:"content"`
	Description string    `json:"description"`
	AgentType   string    `json:"agentType"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

func NewPromptHandlers(store promptstore.Store, queries *sqlc.Queries) rpc.HandlerMapResult {
	return buildPromptHandlers(store, newPromptTxRunner(queries))
}

func buildPromptHandlers(store promptstore.Store, txRunner promptTxRunner) rpc.HandlerMapResult {
	return buildPromptHandlersWithService(newPromptService(store, txRunner))
}

func buildPromptHandlersWithService(promptSvc PromptService) rpc.HandlerMapResult {
	return rpc.HandlerMapResult{Handlers: handler.Map{
		"prompts/list": rpc.StrictHandler(func(ctx context.Context, p promptListParams) (any, error) {
			templates, err := promptSvc.ListPrompts(ctx, p.Cwd, "")
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompts": promptItemsFromTemplates(templates)}, nil
		}),
		"prompts/write": rpc.StrictHandler(func(ctx context.Context, p promptWriteParams) (any, error) {
			template, err := promptSvc.WritePrompt(ctx, p.Cwd, PromptWriteRequest{
				ID:          p.ID,
				Name:        p.Name,
				Content:     p.Content,
				Description: p.Description,
				AgentType:   p.AgentType,
			})
			if err != nil {
				return nil, err
			}
			return map[string]any{"prompt": promptItemFromTemplate(*template)}, nil
		}),
		"prompts/delete": rpc.StrictHandler(func(ctx context.Context, p promptDeleteParams) (any, error) {
			if err := promptSvc.DeletePrompt(ctx, p.Cwd, p.ID); err != nil {
				return nil, err
			}
			return map[string]any{"ok": true}, nil
		}),
	}}
}

func promptItemsFromTemplates(templates []promptstore.PromptTemplate) []promptRPCItem {
	items := make([]promptRPCItem, 0, len(templates))
	for _, template := range templates {
		items = append(items, promptItemFromTemplate(template))
	}
	return items
}

func promptItemFromTemplate(template promptstore.PromptTemplate) promptRPCItem {
	return promptRPCItem{
		ID:          template.PromptKey,
		Name:        template.Title,
		Content:     template.PromptText,
		Description: template.Description,
		AgentType:   template.AgentKey,
		CreatedAt:   template.CreatedAt,
		UpdatedAt:   template.UpdatedAt,
	}
}
