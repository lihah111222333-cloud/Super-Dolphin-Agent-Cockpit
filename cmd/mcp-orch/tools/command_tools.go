package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const resourceListLimit int32 = 50

type commandListInput struct {
	Keyword string `json:"keyword,omitempty"`
}

type commandGetInput struct {
	CardKey string `json:"card_key"`
}

type commandCardDTO struct {
	ID              int64           `json:"id"`
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema,omitempty"`
	RiskLevel       string          `json:"risk_level"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastRunAt       *time.Time      `json:"last_run_at,omitempty"`
	RunCount        int64           `json:"run_count"`
}

func HandleCommandList(store commandcardstore.Store) ToolHandler {
	return makeHandler(store, "command card store", func(ctx context.Context, in commandListInput) ([]commandCardDTO, error) {
		return listCommandCards(ctx, store, in)
	})
}

func HandleCommandGet(store commandcardstore.Store) ToolHandler {
	return makeHandler(store, "command card store", func(ctx context.Context, in commandGetInput) (commandCardDTO, error) {
		return getCommandCard(ctx, store, in)
	})
}

func commandToolDefinitions(store commandcardstore.Store) []ToolDefinition {
	return resourceToolDefinitions(resourceToolSpec{
		ListName:        "command_list",
		ListDescription: "List available command cards. Command cards define reusable operations with templates and argument schemas.",
		GetName:         "command_get",
		GetDescription:  "Get a specific command card by its key, including the command template and argument schema.",
		KeyField:        "card_key",
		KeyDescription:  "Command card key.",
		ListHandler:     HandleCommandList(store),
		GetHandler:      HandleCommandGet(store),
	})
}

func listCommandCards(ctx context.Context, store commandcardstore.Store, input commandListInput) ([]commandCardDTO, error) {
	if err := requireDependency(store, "command card store"); err != nil {
		return nil, err
	}
	cards, err := store.List(ctx, commandcardstore.ListFilter{
		Keyword: strings.TrimSpace(input.Keyword),
		Limit:   resourceListLimit,
	})
	if err != nil {
		return nil, err
	}
	return mapCommandCards(cards), nil
}

func getCommandCard(ctx context.Context, store commandcardstore.Store, input commandGetInput) (commandCardDTO, error) {
	if err := requireDependency(store, "command card store"); err != nil {
		return commandCardDTO{}, err
	}
	cardKey, err := requireTrimmed(input.CardKey, "card_key")
	if err != nil {
		return commandCardDTO{}, err
	}
	card, err := store.Get(ctx, cardKey)
	card, err = loadOrNotFound(card, err, "command", cardKey)
	if err != nil {
		return commandCardDTO{}, err
	}
	return commandCardFromStore(*card), nil
}

func mapCommandCards(cards []commandcardstore.CommandCard) []commandCardDTO {
	mapped := make([]commandCardDTO, 0, len(cards))
	for _, card := range cards {
		mapped = append(mapped, commandCardFromStore(card))
	}
	return mapped
}

func commandCardFromStore(card commandcardstore.CommandCard) commandCardDTO {
	return commandCardDTO{
		ID:              card.ID,
		CardKey:         card.CardKey,
		Title:           card.Title,
		Description:     card.Description,
		CommandTemplate: card.CommandTemplate,
		ArgsSchema:      shared.CloneRawMessage(card.ArgsSchema),
		RiskLevel:       card.RiskLevel,
		Enabled:         card.Enabled,
		CreatedBy:       card.CreatedBy,
		UpdatedBy:       card.UpdatedBy,
		CreatedAt:       card.CreatedAt,
		UpdatedAt:       card.UpdatedAt,
		LastRunAt:       shared.CloneTime(card.LastRunAt),
		RunCount:        card.RunCount,
	}
}
