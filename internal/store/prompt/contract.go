package prompt

import (
	"context"
	"encoding/json"
	"time"
)

// Reader provides read-only access to prompt templates.
// This is the shared interface consumed by both internal modules and cmd/mcp-orch.
type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
}

type ListFilter struct {
	AgentKey string
	Keyword  string
	Limit    int32
}

// PromptTemplate is the shared domain DTO for prompt templates.
type PromptTemplate struct {
	ID          int64           `json:"id"`
	PromptKey   string          `json:"prompt_key"`
	Title       string          `json:"title"`
	AgentKey    string          `json:"agent_key"`
	ToolName    string          `json:"tool_name"`
	PromptText  string          `json:"prompt_text"`
	Variables   json.RawMessage `json:"variables"`
	Tags        json.RawMessage `json:"tags"`
	Enabled     bool            `json:"enabled"`
	CreatedBy   string          `json:"created_by"`
	UpdatedBy   string          `json:"updated_by"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Description string          `json:"description"`
}
