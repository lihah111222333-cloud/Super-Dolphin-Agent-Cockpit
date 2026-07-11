// Package prompt 负责系统提示的注册、组装和缓存，为 provider 提供 start/turn 所需的结构化上下文。
package prompt

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	promptintent "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/prompt/intent"
)

var (
	// ErrStoreNotConfigured 表示 prompt 持久化领域端口未完成装配。
	ErrStoreNotConfigured = errors.New("prompt store is not configured")
	// ErrStoreTxCallbackRequired 表示事务调用缺少领域 callback。
	ErrStoreTxCallbackRequired = errors.New("prompt store tx callback is required")
)

// PromptRegion 标识 prompt section 位于 cacheable prefix 还是动态 tail。
type PromptRegion = contract.PromptRegion

const (
	PromptRegionStatic  = contract.PromptRegionStatic
	PromptRegionDynamic = contract.PromptRegionDynamic
)

// SectionContext 是 section compute 函数收到的完整组装上下文。
type SectionContext = contract.SectionContext

// SectionComputeFunc 是动态或静态 section 的内容生成函数签名。
type SectionComputeFunc = contract.SectionComputeFunc

// PromptSection 描述一个可注册的 prompt section。
type PromptSection = contract.PromptSection

// ResolvedPromptSection 是组装后带内容的 section。
type ResolvedPromptSection = contract.ResolvedPromptSection

// MCPSnapshot 描述当前 MCP server/tool/resource 状态。
type MCPSnapshot = contract.MCPSnapshot

// MCPAttachmentRef 是 MCP 附件在 prompt 中的引用信息。
type MCPAttachmentRef = contract.MCPAttachmentRef

// OutputStyleConfig 描述用户配置的输出风格。
type OutputStyleConfig = contract.OutputStyleConfig

// BuildCtx 是 prompt 组装时使用的运行上下文快照。
type BuildCtx = contract.BuildCtx

// SystemContext 是随 turn 变化的系统上下文键值集合。
type SystemContext = contract.SystemContext

// InvalidateReason 描述 prompt section 缓存失效原因。
type InvalidateReason = contract.InvalidateReason

const (
	InvalidateClear          = contract.InvalidateClear
	InvalidateCompact        = contract.InvalidateCompact
	InvalidateWorktree       = contract.InvalidateWorktree
	InvalidateResumeRestore  = contract.InvalidateResumeRestore
	InvalidateProviderSwitch = contract.InvalidateProviderSwitch
	InvalidateMemoryWrite    = contract.InvalidateMemoryWrite
)

// SnapshotVersion 是 prompt 组装快照的当前版本号。
const SnapshotVersion = contract.PromptAssemblySnapshotVersion

// StartInput 是 start 阶段 prompt 组装输入。
type StartInput = contract.StartInput

// TurnInput 是 turn 阶段 prompt 组装输入。
type TurnInput = contract.TurnInput

// StartAssembly 是 start 阶段 prompt 组装结果。
type StartAssembly = contract.StartAssembly

// TurnAssembly 是 turn 阶段 prompt 组装结果。
type TurnAssembly = contract.TurnAssembly

// PromptAssemblySnapshot 是可持久化的 prompt 组装快照。
type PromptAssemblySnapshot = contract.PromptAssemblySnapshot

// PreferenceReader 是 prompt hint 读取用户偏好的最小边界。
type PreferenceReader interface {
	GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error)
}

// SharedFileReader 是 prompt hint 读取共享文件正文的最小边界。
type SharedFileReader interface {
	GetContent(ctx context.Context, path string) (string, error)
}

// Store 是 prompt 模块拥有的持久化领域端口。
// App 组合边界负责把 concrete Store 适配为该端口，业务文件不能直接感知 Store DTO。
type Store interface {
	List(ctx context.Context, filter ListFilter) ([]Template, error)
	WithTx(ctx context.Context, fn func(txStore Store) error) error
	Get(ctx context.Context, promptKey string) (*Template, error)
	Delete(ctx context.Context, promptKey string) error
	InsertVersion(ctx context.Context, version TemplateVersion) (int64, error)
	CreatePromptTemplate(ctx context.Context, template Template) (*Template, error)
	Upsert(ctx context.Context, template Template) (*Template, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]TemplateSection, error)
	ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]TemplateSection, error)
	ListRecallSections(ctx context.Context, cwd string) ([]TemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]TemplateSection, error)
	UpsertSection(ctx context.Context, section TemplateSection) (*TemplateSection, error)
	DeleteSection(ctx context.Context, templateID int64, sectionKey string) error
	UpsertRecallTopicTargetInCWD(ctx context.Context, cwd, topic string, templateID int64, sectionKey string) error
	UpsertIntentDraft(ctx context.Context, draft IntentDraft) (*IntentDraft, error)
	GetIntentDraft(ctx context.Context, cwd, draftKey string) (*IntentDraft, error)
	ListIntentDrafts(ctx context.Context, filter IntentDraftListFilter) ([]IntentDraft, error)
	UpdateIntentDraftStatus(ctx context.Context, cwd, draftKey, status string) (*IntentDraft, error)
	LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error
}

// ListFilter 限定 prompt 模板列表的 agent、关键词、cwd 与显式上限。
type ListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// Template 是 prompt 模块拥有的模板领域视图。
type Template struct {
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

// TemplateSection 是 prompt 模块拥有的模板 section 领域视图。
type TemplateSection struct {
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

// TemplateVersion 是删除或覆盖模板前保存的版本快照。
type TemplateVersion struct {
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

// IntentDraft 是 prompt intent 草稿在父领域中的稳定视图。
type IntentDraft struct {
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

// IntentDraftListFilter 限定 cwd、状态和显式结果上限。
type IntentDraftListFilter struct {
	CWD    string
	Status string
	Limit  int32
}

// UpsertIntentDraft 将 intent 草稿转换后写入父 store。
func (a promptIntentStoreAdapter) UpsertIntentDraft(
	ctx context.Context,
	draft promptintent.PromptIntentDraft,
) (*promptintent.PromptIntentDraft, error) {
	saved, err := a.store.UpsertIntentDraft(ctx, promptIntentDraftFromIntent(draft))
	if err != nil {
		return nil, err
	}
	return promptIntentDraftPtrFromPrompt(saved), nil
}

// GetIntentDraft 读取单条草稿并转换为 intent 子包 DTO。
func (a promptIntentStoreAdapter) GetIntentDraft(ctx context.Context, cwd, draftKey string) (*promptintent.PromptIntentDraft, error) {
	draft, err := a.store.GetIntentDraft(ctx, cwd, draftKey)
	if err != nil {
		return nil, err
	}
	return promptIntentDraftPtrFromPrompt(draft), nil
}

// ListIntentDrafts 查询草稿列表并转换为 intent 子包 DTO。
func (a promptIntentStoreAdapter) ListIntentDrafts(
	ctx context.Context,
	filter promptintent.PromptIntentDraftListFilter,
) ([]promptintent.PromptIntentDraft, error) {
	drafts, err := a.store.ListIntentDrafts(ctx, IntentDraftListFilter{
		CWD:    filter.CWD,
		Status: filter.Status,
		Limit:  filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return promptIntentDraftsFromPrompt(drafts), nil
}

// UpdateIntentDraftStatus 更新草稿状态并转换保存后的 DTO。
func (a promptIntentStoreAdapter) UpdateIntentDraftStatus(
	ctx context.Context,
	cwd, draftKey, status string,
) (*promptintent.PromptIntentDraft, error) {
	draft, err := a.store.UpdateIntentDraftStatus(ctx, cwd, draftKey, status)
	if err != nil {
		return nil, err
	}
	return promptIntentDraftPtrFromPrompt(draft), nil
}

// LockRecallTopicInCWD 在父 store 中锁定 cwd 内 recall topic。
func (a promptIntentStoreAdapter) LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error {
	return a.store.LockRecallTopicInCWD(ctx, cwd, topic)
}
