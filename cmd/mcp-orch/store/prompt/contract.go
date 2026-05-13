package prompt

import (
	"context"
	"encoding/json"
	"time"
)

type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
}

type ListFilter struct {
	AgentKey string
	Keyword  string
	Limit    int32
}

type PromptTemplate struct {
	ID             int64           `json:"id"`
	PromptKey      string          `json:"prompt_key"`
	Title          string          `json:"title"`
	AgentKey       string          `json:"agent_key"`
	ToolName       string          `json:"tool_name"`
	PromptText     string          `json:"prompt_text"`
	Variables      json.RawMessage `json:"variables"`
	Tags           json.RawMessage `json:"tags"`
	Enabled        bool            `json:"enabled"`
	ManuallyEdited bool            `json:"manually_edited"`
	CreatedBy      string          `json:"created_by"`
	UpdatedBy      string          `json:"updated_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Description    string          `json:"description"`
}

type Store interface {
	Reader
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
}

type PromptTemplateVersion struct {
	ID              int64
	PromptKey       string
	Title           string
	AgentKey        string
	ToolName        string
	PromptText      string
	Variables       json.RawMessage
	Tags            json.RawMessage
	Description     string
	Enabled         bool
	CreatedBy       string
	UpdatedBy       string
	SourceUpdatedAt *time.Time
	CreatedAt       time.Time
	ArchivedAt      time.Time
}
