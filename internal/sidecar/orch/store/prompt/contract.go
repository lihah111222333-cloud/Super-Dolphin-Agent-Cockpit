package prompt

import (
	"context"
	"encoding/json"
	"time"
)

// Reader describes a prompt API type.
type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
}

// ListFilter carries input for prompt operations.
type ListFilter struct {
	AgentKey       string
	Keyword        string
	CWD            string
	RuntimeVisible bool
	Limit          int32
}

// PromptTemplate describes a prompt API type.
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
	WhenToUse      string          `json:"when_to_use"`
	MatchWhen      json.RawMessage `json:"match_when"`
	Priority       int32           `json:"priority"`
}

// PromptTemplateSection describes a prompt API type.
type PromptTemplateSection struct {
	ID          int64  `json:"id"`
	TemplateID  int64  `json:"template_id"`
	SectionKey  string `json:"section_key"`
	Region      string `json:"region"`
	Ordinal     int32  `json:"ordinal"`
	Body        string `json:"body"`
	TriggerType string `json:"trigger_type"`
	RecallTopic string `json:"recall_topic"`
	Enabled     bool   `json:"enabled"`
}

// Store defines persistence operations used by the prompt package.
type Store interface {
	Reader
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	GetSectionByRecallTopic(ctx context.Context, cwd, topic string) (string, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
}

// PromptTemplateVersion describes a prompt API type.
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
