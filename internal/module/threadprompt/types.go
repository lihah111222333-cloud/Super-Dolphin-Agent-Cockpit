package threadprompt

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"time"
)

// PromptStore 是 threadprompt 运行时 catalog 需要的最小 prompt 存储端口。
// 具体 promptstore 只能在 module.go 适配成本接口，业务文件不得直接依赖 store DTO。
type PromptStore interface {
	List(ctx context.Context, filter promptListFilter) ([]PromptTemplate, error)
	Get(ctx context.Context, promptKey string) (*PromptTemplate, error)
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]PromptTemplateSection, error)
	ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
}

// RuntimePromptCatalog 是 threadprompt provider 和路由 adapter 共用的运行时 prompt 目录端口。
type RuntimePromptCatalog interface {
	ListTemplates(ctx context.Context, filter RuntimeListFilter) ([]PromptTemplate, error)
	GetTemplate(ctx context.Context, promptKey, cwd string) (*PromptTemplate, error)
	ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error)
	ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error)
	InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error)
}

// RuntimeListFilter 描述运行时 prompt catalog 的列表过滤条件。
type RuntimeListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

type promptListFilter struct {
	AgentKey string
	Keyword  string
	CWD      string
	Limit    int32
}

// PromptTemplate 是 threadprompt 业务逻辑使用的模板 DTO。
type PromptTemplate struct {
	ID             int64
	PromptKey      string
	Title          string
	AgentKey       string
	ToolName       string
	PromptText     string
	WhenToUse      string
	Variables      json.RawMessage
	Tags           json.RawMessage
	Enabled        bool
	ManuallyEdited bool
	MatchWhen      json.RawMessage
	Priority       int
	CreatedBy      string
	UpdatedBy      string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	Description    string
}

// PromptTemplateSection 是 threadprompt 渲染和意图推断使用的 section DTO。
type PromptTemplateSection struct {
	ID                  int64
	TemplateID          int64
	SectionKey          string
	Region              string
	Ordinal             int
	Body                string
	EnableWhen          json.RawMessage
	Enabled             bool
	TriggerType         string
	RecallTopic         string
	TemplatePromptKey   string
	TemplateTitle       string
	TemplateDescription string
	TemplateWhenToUse   string
	TemplateTags        json.RawMessage
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

// PromptTemplateVersion 是 threadprompt 写入 prompt 版本归档时使用的 DTO。
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

func isRuntimeAssetTemplate(template PromptTemplate) bool {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return true
	}
	for _, tag := range templateTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case "intent:recall", "intent:default_rule":
			return true
		}
	}
	return false
}

func templateTags(raw json.RawMessage) []string {
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		slog.Warn("threadprompt: template tags unmarshal failed, returning nil tag slice",
			slog.Int("raw_len", len(raw)),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return tags
}
