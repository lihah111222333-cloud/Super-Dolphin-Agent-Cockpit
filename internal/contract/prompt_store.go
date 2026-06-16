package contract

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// PromptReader provides read-only access to prompt templates.
type PromptReader interface {
	List(ctx context.Context, filter PromptListFilter) ([]PromptTemplate, error)
}

// PromptStore defines the prompt persistence port consumed by prompt modules.
type PromptStore interface {
	PromptReader
	WithTx(ctx context.Context, fn func(txStore PromptStore) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	CreatePromptTemplate(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
	Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]PromptTemplateSection, error)
	ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	UpsertSection(ctx context.Context, section PromptTemplateSection) (*PromptTemplateSection, error)
	DeleteSection(ctx context.Context, templateID int64, sectionKey string) error
	UpsertRecallTopicTargetInCWD(ctx context.Context, cwd, topic string, templateID int64, sectionKey string) error
	UpsertIntentDraft(ctx context.Context, draft PromptIntentDraft) (*PromptIntentDraft, error)
	GetIntentDraft(ctx context.Context, cwd, draftKey string) (*PromptIntentDraft, error)
	ListIntentDrafts(ctx context.Context, filter PromptIntentDraftListFilter) ([]PromptIntentDraft, error)
	UpdateIntentDraftStatus(ctx context.Context, cwd, draftKey, status string) (*PromptIntentDraft, error)
	LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error
}

// PromptListFilter carries prompt template list filters.
type PromptListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// PromptRuntimeListFilter carries runtime prompt catalog list filters.
type PromptRuntimeListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// RuntimePromptCatalog is the read path used during thread prompt routing.
type RuntimePromptCatalog interface {
	ListTemplates(ctx context.Context, filter PromptRuntimeListFilter) ([]PromptTemplate, error)
	GetTemplate(ctx context.Context, promptKey, cwd string) (*PromptTemplate, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
}

// PromptTemplate is the shared domain DTO for prompt templates.
type PromptTemplate struct {
	ID         int64           `json:"id"`
	PromptKey  string          `json:"prompt_key"`
	Title      string          `json:"title"`
	AgentKey   string          `json:"agent_key"`
	ToolName   string          `json:"tool_name"`
	PromptText string          `json:"prompt_text"`
	WhenToUse  string          `json:"when_to_use"`
	Variables  json.RawMessage `json:"variables"`
	Tags       json.RawMessage `json:"tags"`
	Enabled    bool            `json:"enabled"`
	// ManuallyEdited protects seed-owned prompts from later seed migrations.
	// UI/admin write paths set it true when updating an existing template.
	ManuallyEdited bool `json:"manually_edited"`
	// MatchWhen is the template-level auto-routing rule (JSONB, opt-in).
	// nil means no auto-routing; "{}" means always match.
	MatchWhen json.RawMessage `json:"match_when,omitempty"`
	// Priority is the tie-break key when multiple templates match.
	Priority    int       `json:"priority"`
	CreatedBy   string    `json:"created_by"`
	UpdatedBy   string    `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Description string    `json:"description"`
}

// PromptTemplateSection is a single ordered block within a prompt template.
type PromptTemplateSection struct {
	ID          int64           `json:"id"`
	TemplateID  int64           `json:"template_id"`
	SectionKey  string          `json:"section_key"`
	Region      string          `json:"region"`
	Ordinal     int             `json:"ordinal"`
	Body        string          `json:"body"`
	EnableWhen  json.RawMessage `json:"enable_when,omitempty"`
	Enabled     bool            `json:"enabled"`
	TriggerType string          `json:"trigger_type"`
	RecallTopic string          `json:"recall_topic"`
	// TemplatePromptKey is populated by cross-template queries such as
	// ListRecallSections; per-template section queries leave it empty.
	TemplatePromptKey   string `json:"template_prompt_key,omitempty"`
	TemplateTitle       string `json:"template_title,omitempty"`
	TemplateDescription string `json:"template_description,omitempty"`
	TemplateWhenToUse   string `json:"template_when_to_use,omitempty"`
	TemplateTags        json.RawMessage
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// PromptTemplateVersion is a persisted historical prompt template snapshot.
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

// PromptIntentDraft is a persisted generated prompt asset draft.
type PromptIntentDraft struct {
	ID            int64
	DraftKey      string
	CWD           string
	Kind          string
	RawInput      string
	SourceType    string
	SourceURL     string
	OriginHash    string
	LicenseHint   string
	GeneratedCard json.RawMessage
	Confidence    float64
	Status        string
	Scope         string
	Issues        json.RawMessage
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// PromptIntentDraftListFilter carries prompt intent draft list filters.
type PromptIntentDraftListFilter struct {
	CWD    string
	Status string
	Limit  int32
}

// IsRuntimeAssetPromptTemplate reports whether a prompt template is a runtime asset.
func IsRuntimeAssetPromptTemplate(template PromptTemplate) bool {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return true
	}
	for _, tag := range PromptTemplateTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case "intent:recall", "intent:default_rule":
			return true
		}
	}
	return false
}

// PromptTemplateTags decodes the JSON tag array stored on a prompt template.
func PromptTemplateTags(raw json.RawMessage) []string {
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		return nil
	}
	return tags
}
