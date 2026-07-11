package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	commandcardstore "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/commandcard"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const resourceListLimit int32 = 50

type commandListInput struct {
	Keyword  string `json:"keyword,omitempty"`
	Envelope bool   `json:"envelope,omitempty"`
}

type commandGetInput struct {
	CardKey string `json:"card_key"`
	Pos     string `json:"pos,omitempty"`
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

// CommandListOutput 是 command_list envelope 响应，保留 Commands/Data 双字段兼容旧调用方。
type CommandListOutput struct {
	Commands  []commandCardDTO `json:"commands"`
	Data      []commandCardDTO `json:"data"`
	Total     int              `json:"total"`
	Showing   int              `json:"showing"`
	Truncated bool             `json:"truncated"`
	Hint      string           `json:"hint,omitempty"`
}

// HandleCommandList 列出 command card，可按 keyword 过滤并保留旧数组返回形态。
func HandleCommandList(store commandcardstore.Store) ToolHandler {
	return makeHandler(store, "command card store", func(ctx context.Context, in commandListInput) (any, error) {
		cards, err := listCommandCards(ctx, store, in)
		if err != nil {
			return nil, err
		}
		if in.Envelope {
			return newCommandListOutput(cards), nil
		}
		return cards, nil
	})
}

// HandleCommandGet 读取单张 command card，支持 pos=command:<card_key> 与旧 card_key 字段。
func HandleCommandGet(store commandcardstore.Store) ToolHandler {
	return makeHandler(store, "command card store", func(ctx context.Context, in commandGetInput) (commandCardDTO, error) {
		return getCommandCard(ctx, store, in)
	})
}

// commandToolDefinitions 注册 command_list/command_get 这一组资源工具。
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

// listCommandCards 从 store 读取最多 resourceListLimit 条 command card 并映射为工具 DTO。
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

// newCommandListOutput 构造 envelope 返回，同时保留 commands 字段兼容旧调用方。
func newCommandListOutput(cards []commandCardDTO) CommandListOutput {
	env := newListEnvelope(cards, int(resourceListLimit), "next: use command_get pos=command:<card_key> for command details")
	return CommandListOutput{
		Commands:  cards,
		Data:      env.Data,
		Total:     env.Total,
		Showing:   env.Showing,
		Truncated: env.Truncated,
		Hint:      env.Hint,
	}
}

// getCommandCard 解析兼容定位字段并把 not-found 统一成工具层错误。
func getCommandCard(ctx context.Context, store commandcardstore.Store, input commandGetInput) (commandCardDTO, error) {
	if err := requireDependency(store, "command card store"); err != nil {
		return commandCardDTO{}, err
	}
	cardKey, err := resolveCommandKeyInput(input.CardKey, input.Pos)
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

// mapCommandCards 批量映射 store 行，保持返回顺序不变。
func mapCommandCards(cards []commandcardstore.CommandCard) []commandCardDTO {
	mapped := make([]commandCardDTO, 0, len(cards))
	for _, card := range cards {
		mapped = append(mapped, commandCardFromStore(card))
	}
	return mapped
}

// commandCardFromStore 把持久化 command card 映射成 MCP wire DTO。
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
