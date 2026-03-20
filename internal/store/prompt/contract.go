package prompt

import (
	"context"
	"encoding/json"
	"time"
)

type Store interface {
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	InsertVersion(ctx context.Context, version PromptTemplateVersion) error
	Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
}

type ListFilter struct {
	AgentKey string
	Keyword  string
	Limit    int32
}

type PromptTemplate struct {
	ID          int64
	PromptKey   string
	Title       string
	AgentKey    string
	ToolName    string
	PromptText  string
	Variables   json.RawMessage
	Tags        json.RawMessage
	Enabled     bool
	CreatedBy   string
	UpdatedBy   string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Description string
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
	Enabled         bool
	CreatedBy       string
	UpdatedBy       string
	SourceUpdatedAt *time.Time
	CreatedAt       time.Time
	ArchivedAt      time.Time
}
