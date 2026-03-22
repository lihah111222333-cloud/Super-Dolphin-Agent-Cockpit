package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	commandcardstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/commandcard"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
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
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in commandListInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return listCommandCards(ctx, store, in)
	}
}

func HandleCommandGet(store commandcardstore.Store) ToolHandler {
	return func(ctx context.Context, input json.RawMessage) (any, error) {
		var in commandGetInput
		if err := decodeInput(input, &in); err != nil {
			return nil, err
		}
		return getCommandCard(ctx, store, in)
	}
}

func commandToolDefinitions(store commandcardstore.Store) []ToolDefinition {
	return []ToolDefinition{
		{
			Name:        "command_list",
			Description: "List available command cards. Command cards define reusable operations with templates and argument schemas.",
			InputSchema: ObjectSchema(map[string]Schema{
				"keyword": StringSchema("Search keyword (optional)."),
			}),
			Handler: HandleCommandList(store),
		},
		{
			Name:        "command_get",
			Description: "Get a specific command card by its key, including the command template and argument schema.",
			InputSchema: ObjectSchema(map[string]Schema{
				"card_key": StringSchema("Command card key."),
			}, "card_key"),
			Handler: HandleCommandGet(store),
		},
	}
}

func listCommandCards(ctx context.Context, store commandcardstore.Store, input commandListInput) ([]commandCardDTO, error) {
	if store == nil {
		return nil, errors.New("command card store is not configured")
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
	if store == nil {
		return commandCardDTO{}, errors.New("command card store is not configured")
	}
	cardKey, err := requireTrimmed(input.CardKey, "card_key")
	if err != nil {
		return commandCardDTO{}, err
	}
	card, err := store.Get(ctx, cardKey)
	if err != nil {
		if platformdb.IsNotFound(err) {
			return commandCardDTO{}, fmt.Errorf("command %s not found", cardKey)
		}
		return commandCardDTO{}, err
	}
	if card == nil {
		return commandCardDTO{}, fmt.Errorf("command %s not found", cardKey)
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
		ArgsSchema:      cloneRawMessage(card.ArgsSchema),
		RiskLevel:       card.RiskLevel,
		Enabled:         card.Enabled,
		CreatedBy:       card.CreatedBy,
		UpdatedBy:       card.UpdatedBy,
		CreatedAt:       card.CreatedAt,
		UpdatedAt:       card.UpdatedAt,
		LastRunAt:       cloneTime(card.LastRunAt),
		RunCount:        card.RunCount,
	}
}

func cloneRawMessage(src json.RawMessage) json.RawMessage {
	if len(src) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), src...)
}

func cloneTime(src *time.Time) *time.Time {
	if src == nil {
		return nil
	}
	value := *src
	return &value
}

func requireTrimmed(value, field string) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", errors.New(field + " is required")
	}
	return trimmed, nil
}
