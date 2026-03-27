package prompt

import (
	"context"
	"encoding/json"
	"time"

	pt "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// Re-export shared types from internal/store/prompt.
type PromptTemplate = pt.PromptTemplate
type ListFilter = pt.ListFilter

// Store extends the shared Reader with write operations.
type Store interface {
	pt.Reader
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version PromptTemplateVersion) error
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
