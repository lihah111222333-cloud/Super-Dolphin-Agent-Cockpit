package intent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// Kind 是提示词意图的类型枚举，决定草稿卡片的校验规则和提交路径。
type Kind string

const (
	KindExpert      Kind = "expert"       // 专家能力：有具体执行步骤和输出格式。
	KindRecall      Kind = "recall"       // 知识资料：按主题索引，供模型查阅。
	KindDefaultRule Kind = "default_rule" // 默认规则：作为项目级全局约束。
)

// DraftParams 是创建草稿的请求参数。
type DraftParams struct {
	Kind          string `json:"kind"`
	RawInput      string `json:"raw_input"`
	Cwd           string `json:"cwd,omitempty"`
	SourceType    string `json:"source_type,omitempty"`
	SourceURL     string `json:"source_url,omitempty"`
	LicenseHint   string `json:"license_hint,omitempty"`
	EnableGlobal  bool   `json:"enable_global,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Model         string `json:"model,omitempty"`
	ModelProvider string `json:"model_provider,omitempty"`
}

// DryRunParams 是试问（dry-run）请求参数。
type DryRunParams struct {
	DraftKey string          `json:"draft_key,omitempty"`
	Kind     string          `json:"kind"`
	Card     json.RawMessage `json:"card"`
	Question string          `json:"question"`
	Cwd      string          `json:"cwd,omitempty"`
}

// CommitParams 是提交草稿请求参数。
type CommitParams struct {
	DraftKey      string `json:"draft_key"`
	Cwd           string `json:"cwd,omitempty"`
	ConfirmRisk   bool   `json:"confirm_risk,omitempty"`
	EnableGlobal  bool   `json:"enable_global,omitempty"`
	ConfirmGlobal bool   `json:"confirm_global,omitempty"`
}

// DiscardParams 是丢弃草稿请求参数。
type DiscardParams struct {
	DraftKey string `json:"draft_key"`
	Cwd      string `json:"cwd,omitempty"`
}

// E2EHealthParams 是端到端健康检查请求参数（无字段）。
type E2EHealthParams struct{}

// E2EHealthResult 是端到端健康检查返回结果。
type E2EHealthResult struct {
	Provider        string `json:"provider"`
	FixturePathHash string `json:"fixture_path_hash,omitempty"`
}

// Store 是 prompt intent 读写草稿、模板和 section 的窄持久化边界。
// App 组合边界先把 concrete Store 适配为父 prompt 领域端口，再由父领域适配到本接口。
type Store interface {
	List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error)
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	CreatePromptTemplate(ctx context.Context, template PromptTemplate) (*PromptTemplate, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]PromptTemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	UpsertSection(ctx context.Context, section PromptTemplateSection) (*PromptTemplateSection, error)
	UpsertRecallTopicTargetInCWD(ctx context.Context, cwd, topic string, templateID int64, sectionKey string) error
	UpsertIntentDraft(ctx context.Context, draft PromptIntentDraft) (*PromptIntentDraft, error)
	GetIntentDraft(ctx context.Context, cwd, draftKey string) (*PromptIntentDraft, error)
	ListIntentDrafts(ctx context.Context, filter PromptIntentDraftListFilter) ([]PromptIntentDraft, error)
	UpdateIntentDraftStatus(ctx context.Context, cwd, draftKey, status string) (*PromptIntentDraft, error)
	LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error
}

// ListFilter 限定 intent 创建/提交路径读取的 prompt 模板范围。
type ListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// PromptTemplate 是 intent 子包内部使用的模板 DTO，由父包 adapter 转换自持久化层。
type PromptTemplate struct {
	ID             int64           `json:"id"`
	PromptKey      string          `json:"prompt_key"`
	Title          string          `json:"title"`
	AgentKey       string          `json:"agent_key"`
	ToolName       string          `json:"tool_name"`
	PromptText     string          `json:"prompt_text"`
	WhenToUse      string          `json:"when_to_use"`
	Variables      json.RawMessage `json:"variables"`
	Tags           json.RawMessage `json:"tags"`
	Enabled        bool            `json:"enabled"`
	ManuallyEdited bool            `json:"manually_edited"`
	MatchWhen      json.RawMessage `json:"match_when,omitempty"`
	Priority       int             `json:"priority"`
	CreatedBy      string          `json:"created_by"`
	UpdatedBy      string          `json:"updated_by"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	Description    string          `json:"description"`
}

// PromptTemplateSection 是 intent 子包内部使用的 section DTO。
type PromptTemplateSection struct {
	ID                  int64           `json:"id"`
	TemplateID          int64           `json:"template_id"`
	SectionKey          string          `json:"section_key"`
	Region              string          `json:"region"`
	Ordinal             int             `json:"ordinal"`
	Body                string          `json:"body"`
	EnableWhen          json.RawMessage `json:"enable_when,omitempty"`
	Enabled             bool            `json:"enabled"`
	TriggerType         string          `json:"trigger_type"`
	RecallTopic         string          `json:"recall_topic"`
	TemplatePromptKey   string          `json:"template_prompt_key,omitempty"`
	TemplateTitle       string          `json:"template_title,omitempty"`
	TemplateDescription string          `json:"template_description,omitempty"`
	TemplateWhenToUse   string          `json:"template_when_to_use,omitempty"`
	TemplateTags        json.RawMessage
	CreatedAt           time.Time `json:"created_at"`
	UpdatedAt           time.Time `json:"updated_at"`
}

// PromptTemplateVersion 是 intent 更新路径写入版本历史的 DTO。
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

// PromptIntentDraft 是 intent 草稿 DTO，GeneratedCard 和 Issues 保留原始 JSON 供前端复查。
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

// PromptIntentDraftListFilter 限定 intent draft 查询范围。
type PromptIntentDraftListFilter struct {
	CWD    string
	Status string
	Limit  int32
}

// Card 是提示词意图草稿卡片，保存 LLM 生成的结构化内容；不同 kind 使用不同字段子集。
type Card struct {
	Kind                 string         `json:"kind"`
	Title                string         `json:"title"`
	Summary              string         `json:"summary"`
	WhenToUse            string         `json:"when_to_use,omitempty"`
	WhenNotToUse         string         `json:"when_not_to_use,omitempty"`
	Workflow             []string       `json:"workflow,omitempty"`
	Constraints          []string       `json:"constraints,omitempty"`
	Output               string         `json:"output,omitempty"`
	SaveBoundary         string         `json:"save_boundary,omitempty"`
	RecallTopic          string         `json:"recall_topic,omitempty"`
	RecallBody           string         `json:"recall_body,omitempty"`
	DefaultRuleBody      string         `json:"default_rule_body,omitempty"`
	SourceProfile        string         `json:"source_profile,omitempty"`
	SourceFacts          []SourceFact   `json:"source_facts,omitempty"`
	HitExamples          []string       `json:"hit_examples"`
	MissExamples         []string       `json:"miss_examples"`
	ConflictingRules     []RuleConflict `json:"conflicting_rules,omitempty"`
	SuggestedAlternative *Alternative   `json:"suggested_alternative,omitempty"`
}

// Issue 表示草稿校验发现的问题，Severity 为 "block" 时阻止提交，"review" 时需用户确认。
type Issue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
}

// RuleConflict 记录与已有默认规则的冲突信息。
type RuleConflict struct {
	Title   string `json:"title"`
	Summary string `json:"summary"`
}

// SourceFact 是从原文提取的单条关键要点，disposition 决定是保留、转写还是丢弃。
type SourceFact struct {
	Category    string `json:"category"`
	Summary     string `json:"summary"`
	Disposition string `json:"disposition"`
}

// Alternative 是 LLM 推荐的备选 kind 及原因，当 inferred_kind 与 requested_kind 不同时填入。
type Alternative struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// requireCWD 检查 cwd 非空，否则返回错误；用于所有需要工作目录的 RPC 入口。
func requireCWD(cwd string) (string, error) {
	requestScope := strings.TrimSpace(cwd)
	if requestScope == "" {
		return "", errors.New("dashboard: cwd is required")
	}
	return requestScope, nil
}
