package prompt

import (
	"context"
	"encoding/json"
	"time"
)

// Reader 提供 prompt template 的只读访问边界。
// 内部模块和 mcp-orch 共用该接口，避免直接依赖 sqlc 行类型。
type Reader interface {
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
}

// Store 是 prompt template、section、版本和 intent draft 的完整存储接口。
// 写路径需要保留事务能力，动态 recall 相关方法必须按 cwd 隔离。
type Store interface {
	Reader
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	CreatePromptTemplate(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
	Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
	// ListSectionsByTemplateID 读取指定模板的有序 section。
	// 返回值包含 disabled 行，便于 UI 重新启用；空切片表示该模板仍只使用 PromptTemplate.PromptText。
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	// ListSectionsByTemplateIDs 批量读取多个模板的有序 section。
	// 语义与单模板查询一致，注入路径仍需自行过滤 disabled、recall 和空正文 section。
	ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]PromptTemplateSection, error)
	// ListRecallSections 读取当前 cwd 下启用的 recall section。
	// 结果用于运行时 recall_catalog 动态段，不能跨工作目录混用。
	ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	// UpsertSection 按 (template_id, section_key) 写入单个 prompt_template_section。
	// 写入前会校验 region、trigger_type 和 recall_topic，防止动态段配置落库后才失败。
	UpsertSection(ctx context.Context, section PromptTemplateSection) (*PromptTemplateSection, error)
	// DeleteSection 按 (template_id, section_key) 删除 section。
	// 未命中时返回 platformdb.ErrNotFound，调用方可区分重复删除和真实删除。
	DeleteSection(ctx context.Context, templateID int64, sectionKey string) error
	UpsertRecallTopicTargetInCWD(ctx context.Context, cwd, topic string, templateID int64, sectionKey string) error
	UpsertIntentDraft(ctx context.Context, draft PromptIntentDraft) (*PromptIntentDraft, error)
	GetIntentDraft(ctx context.Context, cwd, draftKey string) (*PromptIntentDraft, error)
	ListIntentDrafts(ctx context.Context, filter PromptIntentDraftListFilter) ([]PromptIntentDraft, error)
	UpdateIntentDraftStatus(ctx context.Context, cwd, draftKey, status string) (*PromptIntentDraft, error)
	LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error
}

// ListFilter 限定 prompt template 管理视图的列表查询。
// CWD 用于工作区隔离，Limit 由调用方显式传入以避免无界扫描。
type ListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// RuntimeListFilter 限定运行时 prompt catalog 的列表查询。
// 它与管理视图 filter 分离，便于运行时按 cwd 和 agentKey 读取可用模板。
type RuntimeListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// RuntimePromptCatalog 是 prompt assembler 运行时读取模板和 section 的窄接口。
// 该接口只暴露组装需要的读能力和版本归档入口，不包含 UI 管理写操作。
type RuntimePromptCatalog interface {
	ListTemplates(ctx context.Context, filter RuntimeListFilter) ([]PromptTemplate, error)
	GetTemplate(ctx context.Context, promptKey, cwd string) (*PromptTemplate, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
}

// PromptTemplate 是 prompt template 的跨模块 DTO。
// JSON 字段名同时供 UI、mcp-orch 和内部运行时读取，不能泄露 sqlc 行类型。
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
	// ManuallyEdited 表示该模板已由 UI 或管理路径手工改写。
	// 种子同步遇到 true 时不能覆盖用户修改。
	ManuallyEdited bool `json:"manually_edited"`
	// MatchWhen 是模板级自动路由规则（JSONB，显式启用）。
	// nil → 不参与自动路由（只能显式指定或走默认模板）
	// "{}" → 永远匹配（参与竞争但无筛选条件，用 priority 处理并列）
	// JSON  → 当 BuildCtx 满足所有键时匹配
	// 路由在显式指定之后、默认模板之前评估；
	// 与段级 section.enable_when 独立，名字相似但语义不同。
	MatchWhen json.RawMessage `json:"match_when,omitempty"`
	// Priority 是多个 match_when 同时命中时的排序键。
	// 值越大优先级越高；默认模板应保持 0 或最低值。
	Priority    int       `json:"priority"`
	CreatedBy   string    `json:"created_by"`
	UpdatedBy   string    `json:"updated_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	Description string    `json:"description"`
}

// PromptTemplateSection 是 prompt template 内的有序段落 DTO。
// region=="static" 进入可缓存前缀，region=="dynamic" 进入非缓存尾部；注入路径负责过滤 enabled 和 enable_when。
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
	// TemplatePromptKey 由跨模板查询填充，例如 ListRecallSections。
	// 单模板 section 查询会保持为空，调用方不能把它当作必填字段。
	TemplatePromptKey   string `json:"template_prompt_key,omitempty"`
	TemplateTitle       string `json:"template_title,omitempty"`
	TemplateDescription string `json:"template_description,omitempty"`
	TemplateWhenToUse   string `json:"template_when_to_use,omitempty"`
	TemplateTags        json.RawMessage
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// PromptTemplateVersion 是 prompt 版本归档 DTO。
// SourceUpdatedAt 为空表示没有可比较的源更新时间，ArchivedAt 由归档路径写入。
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

// PromptIntentDraft 是 prompt intent 导入流程的草稿 DTO。
// CWD 和 DraftKey 共同限定唯一草稿，GeneratedCard 与 Issues 以 JSON 原文跨模块传递。
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

// PromptIntentDraftListFilter 限定 intent draft 列表查询。
// CWD 必填，Limit 必须显式给出，避免跨工作区扫描草稿表。
type PromptIntentDraftListFilter struct {
	CWD    string
	Status string
	Limit  int32
}
