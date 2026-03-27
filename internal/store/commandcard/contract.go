package commandcard

import (
	"context"
	"encoding/json"
	"time"
)

// Reader provides read-only access to command cards.
// This is the shared interface consumed by both internal modules and cmd/mcp-orch.
type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]CommandCard, error)
}

type ListFilter struct {
	Keyword string
	Limit   int32
}

// CommandCard is the shared domain DTO for command cards.
type CommandCard struct {
	ID              int64           `json:"id"`
	CardKey         string          `json:"card_key"`
	Title           string          `json:"title"`
	Description     string          `json:"description"`
	CommandTemplate string          `json:"command_template"`
	ArgsSchema      json.RawMessage `json:"args_schema"`
	RiskLevel       string          `json:"risk_level"`
	Enabled         bool            `json:"enabled"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
	LastRunAt       *time.Time      `json:"last_run_at,omitempty"`
	RunCount        int64           `json:"run_count"`
}
