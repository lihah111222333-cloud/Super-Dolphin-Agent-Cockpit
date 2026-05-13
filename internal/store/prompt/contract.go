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

type Store interface {
	Reader
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
	// ListSectionsByTemplateID returns the ordered enabled sections for the
	// given template. Empty slice means the template has not been migrated to
	// the sectioned layout yet; callers should fall back to PromptTemplate.PromptText.
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	// UpsertSection inserts or updates a single prompt_template_section row by
	// (template_id, section_key). The UI-level "high-advanced debug" editor
	// drives this; ordinary users never see it.
	UpsertSection(ctx context.Context, section PromptTemplateSection) (*PromptTemplateSection, error)
	// DeleteSection removes a section by (template_id, section_key). Returns
	// platformdb.ErrNotFound when the pair does not match a row.
	DeleteSection(ctx context.Context, templateID int64, sectionKey string) error
}

type ListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// PromptTemplate is the shared domain DTO for prompt templates.
type PromptTemplate struct {
	ID         int64           `json:"id"`
	PromptKey  string          `json:"prompt_key"`
	Title      string          `json:"title"`
	AgentKey   string          `json:"agent_key"`
	ToolName   string          `json:"tool_name"`
	PromptText string          `json:"prompt_text"`
	Variables  json.RawMessage `json:"variables"`
	Tags       json.RawMessage `json:"tags"`
	Enabled    bool            `json:"enabled"`
	// ManuallyEdited protects seed-owned prompts from later seed migrations.
	// UI/admin write paths set it true when updating an existing template.
	ManuallyEdited bool `json:"manually_edited"`
	// MatchWhen is the template-level auto-routing rule (JSONB, opt-in).
	// nil → 不参与自动路由（只能 pin / 分类器命中）
	// "{}" → 永远匹配（参与竞争但无筛选条件，用 priority 平溢）
	// JSON  → 当 BuildCtx 满足所有键时匹配
	// 路由在分类器之后、main/default fallback 之前评估；
	// 与段级 section.enable_when 独立，名字相似但语义不同。
	MatchWhen json.RawMessage `json:"match_when,omitempty"`
	// Priority is the tie-break key when multiple templates' match_when all fire.
	// Higher wins. Default 0 (main/default 应保持 0 或最低)。
	Priority    int       `json:"priority"`
	CreatedBy   string    `json:"created_by"`
	UpdatedBy   string    `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Description string    `json:"description"`
}

// PromptTemplateSection is a single ordered block within a prompt template.
// Sections with region=="static" contribute to the cached prefix; region=="dynamic"
// contributes to the uncached tail. EnableWhen is reserved for Step 2
// feature-gate DSL (unused in Step 1 — all enabled sections are emitted).
type PromptTemplateSection struct {
	ID         int64           `json:"id"`
	TemplateID int64           `json:"template_id"`
	SectionKey string          `json:"section_key"`
	Region     string          `json:"region"`
	Ordinal    int             `json:"ordinal"`
	Body       string          `json:"body"`
	EnableWhen json.RawMessage `json:"enable_when,omitempty"`
	Enabled    bool            `json:"enabled"`
	CreatedAt  time.Time       `json:"created_at"`
	UpdatedAt  time.Time       `json:"updated_at"`
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
