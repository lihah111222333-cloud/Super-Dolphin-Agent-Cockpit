package threadadapter

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"go.uber.org/fx"
)

var _ threadprompt.PromptStore = (*threadPromptStoreAdapter)(nil)
var _ thread.PromptCatalog = (*threadPromptCatalogAdapter)(nil)

type threadPromptStoreAdapter struct {
	store promptstore.Store
}

type threadPromptCatalogAdapter struct {
	catalog threadprompt.RuntimePromptCatalog
}

type threadPromptStoreParams struct {
	fx.In

	Store promptstore.Store `optional:"true"`
}

type threadPromptRuntimeCatalogParams struct {
	fx.In

	Store   threadprompt.PromptStore       `optional:"true"`
	Builtin contract.BuiltinPromptRegistry `optional:"true"`
}

type threadPromptRegistrationParams struct {
	fx.In

	Registrar contract.DynamicSectionRegistrar  `optional:"true"`
	Catalog   threadprompt.RuntimePromptCatalog `optional:"true"`
}

// provideThreadPromptStoreAdapter 构造 threadprompt 自有的可选持久化端口。
// 缺少 Store 时保留 nil，让 builtin-only catalog 继续作为合法只读能力。
func provideThreadPromptStoreAdapter(params threadPromptStoreParams) (threadprompt.PromptStore, error) {
	if params.Store == nil {
		return nil, nil
	}
	return &threadPromptStoreAdapter{store: params.Store}, nil
}

// provideThreadPromptRuntimeCatalog 组合数据库 prompt 端口与可选 builtin registry。
func provideThreadPromptRuntimeCatalog(params threadPromptRuntimeCatalogParams) threadprompt.RuntimePromptCatalog {
	return threadprompt.NewRuntimeCatalog(params.Store, params.Builtin)
}

// provideThreadPromptCatalog 将 threadprompt catalog 收窄成 Thread-owned 路由端口。
func provideThreadPromptCatalog(catalog threadprompt.RuntimePromptCatalog) (thread.PromptCatalog, error) {
	if catalog == nil {
		return nil, nil
	}
	return &threadPromptCatalogAdapter{catalog: catalog}, nil
}

// registerThreadPromptProvidersFromApp 在组合根注册动态 prompt providers。
func registerThreadPromptProvidersFromApp(params threadPromptRegistrationParams) error {
	return threadprompt.RegisterProviders(params.Registrar, params.Catalog)
}

// List 转换过滤条件并返回 threadprompt-owned 模板 DTO。
func (a *threadPromptStoreAdapter) List(ctx context.Context, filter threadprompt.PromptListFilter) ([]threadprompt.PromptTemplate, error) {
	rows, err := a.store.List(ctx, threadPromptListFilterToStore(filter))
	if err != nil {
		return nil, err
	}
	return threadPromptTemplatesFromStore(rows), nil
}

// Get 读取并转换单个 prompt 模板。
func (a *threadPromptStoreAdapter) Get(ctx context.Context, promptKey string) (*threadprompt.PromptTemplate, error) {
	row, err := a.store.Get(ctx, promptKey)
	return threadPromptTemplateFromStore(row), err
}

// InsertVersion 转换并写入 prompt 版本归档。
func (a *threadPromptStoreAdapter) InsertVersion(ctx context.Context, version threadprompt.PromptTemplateVersion) (int64, error) {
	return a.store.InsertVersion(ctx, threadPromptVersionToStore(version))
}

// ListSectionsByTemplateID 读取并转换单模板 sections。
func (a *threadPromptStoreAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	return threadPromptSectionsFromStore(rows), nil
}

// ListSectionsByTemplateIDs 批量读取并转换模板 sections。
func (a *threadPromptStoreAdapter) ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateIDs(ctx, templateIDs)
	if err != nil {
		return nil, err
	}
	return threadPromptSectionsFromStore(rows), nil
}

// ListRecallSections 读取并转换工作区 recall sections。
func (a *threadPromptStoreAdapter) ListRecallSections(ctx context.Context, cwd string) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListRecallSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return threadPromptSectionsFromStore(rows), nil
}

// ListDefaultRuleSections 读取并转换工作区默认规则 sections。
func (a *threadPromptStoreAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]threadprompt.PromptTemplateSection, error) {
	rows, err := a.store.ListDefaultRuleSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return threadPromptSectionsFromStore(rows), nil
}

// ListTemplates 将 threadprompt catalog 结果转换成 Thread-owned DTO。
func (a *threadPromptCatalogAdapter) ListTemplates(ctx context.Context, filter thread.PromptListFilter) ([]thread.PromptTemplate, error) {
	rows, err := a.catalog.ListTemplates(ctx, threadPromptListFilterFromThread(filter))
	if err != nil {
		return nil, err
	}
	out := make([]thread.PromptTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, threadPromptTemplateToThread(row))
	}
	return out, nil
}

func threadPromptListFilterToStore(filter threadprompt.PromptListFilter) promptstore.ListFilter {
	return promptstore.ListFilter{AgentKey: filter.AgentKey, Keyword: filter.Keyword, CWD: filter.CWD, Limit: filter.Limit}
}

func threadPromptListFilterFromThread(filter thread.PromptListFilter) threadprompt.RuntimeListFilter {
	return threadprompt.RuntimeListFilter{AgentKey: filter.AgentKey, Keyword: filter.Keyword, CWD: filter.CWD, Limit: filter.Limit}
}

// ListSectionsByTemplateID 将 threadprompt sections 转换成 Thread-owned DTO。
func (a *threadPromptCatalogAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]thread.PromptTemplateSection, error) {
	rows, err := a.catalog.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	out := make([]thread.PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		out = append(out, threadPromptSectionToThread(row))
	}
	return out, nil
}

// InsertVersion 将 Thread-owned 版本 DTO 转换后写入 catalog。
func (a *threadPromptCatalogAdapter) InsertVersion(ctx context.Context, version thread.PromptTemplateVersion) (int64, error) {
	return a.catalog.InsertVersion(ctx, threadPromptVersionFromThread(version))
}

// CanInsertPromptVersion 显式透传 catalog 的写能力，不提供默认 true。
func (a *threadPromptCatalogAdapter) CanInsertPromptVersion() bool {
	return a.catalog.CanInsertPromptVersion()
}

func threadPromptTemplatesFromStore(rows []promptstore.PromptTemplate) []threadprompt.PromptTemplate {
	out := make([]threadprompt.PromptTemplate, 0, len(rows))
	for i := range rows {
		out = append(out, *threadPromptTemplateFromStore(&rows[i]))
	}
	return out
}

func threadPromptTemplateFromStore(row *promptstore.PromptTemplate) *threadprompt.PromptTemplate {
	if row == nil {
		return nil
	}
	return &threadprompt.PromptTemplate{
		ID: row.ID, PromptKey: row.PromptKey, Title: row.Title, AgentKey: row.AgentKey,
		ToolName: row.ToolName, PromptText: row.PromptText, WhenToUse: row.WhenToUse,
		Variables: cloneAdapterJSON(row.Variables), Tags: cloneAdapterJSON(row.Tags), Enabled: row.Enabled,
		ManuallyEdited: row.ManuallyEdited, MatchWhen: cloneAdapterJSON(row.MatchWhen), Priority: row.Priority,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, Description: row.Description,
	}
}

func threadPromptSectionsFromStore(rows []promptstore.PromptTemplateSection) []threadprompt.PromptTemplateSection {
	out := make([]threadprompt.PromptTemplateSection, 0, len(rows))
	for i := range rows {
		out = append(out, threadPromptSectionFromStore(rows[i]))
	}
	return out
}

func threadPromptSectionFromStore(row promptstore.PromptTemplateSection) threadprompt.PromptTemplateSection {
	return threadprompt.PromptTemplateSection{
		ID: row.ID, TemplateID: row.TemplateID, SectionKey: row.SectionKey, Region: row.Region,
		Ordinal: row.Ordinal, Body: row.Body, EnableWhen: cloneAdapterJSON(row.EnableWhen), Enabled: row.Enabled,
		TriggerType: row.TriggerType, RecallTopic: row.RecallTopic, TemplatePromptKey: row.TemplatePromptKey,
		TemplateTitle: row.TemplateTitle, TemplateDescription: row.TemplateDescription,
		TemplateWhenToUse: row.TemplateWhenToUse, TemplateTags: cloneAdapterJSON(row.TemplateTags),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func threadPromptVersionToStore(row threadprompt.PromptTemplateVersion) promptstore.PromptTemplateVersion {
	return promptstore.PromptTemplateVersion{
		ID: row.ID, PromptKey: row.PromptKey, Title: row.Title, AgentKey: row.AgentKey,
		ToolName: row.ToolName, PromptText: row.PromptText, Variables: cloneAdapterJSON(row.Variables),
		Tags: cloneAdapterJSON(row.Tags), Description: row.Description, Enabled: row.Enabled,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, SourceUpdatedAt: row.SourceUpdatedAt,
		CreatedAt: row.CreatedAt, ArchivedAt: row.ArchivedAt,
	}
}

func threadPromptTemplateToThread(row threadprompt.PromptTemplate) thread.PromptTemplate {
	return thread.PromptTemplate{
		ID: row.ID, PromptKey: row.PromptKey, Title: row.Title, AgentKey: row.AgentKey,
		ToolName: row.ToolName, PromptText: row.PromptText, WhenToUse: row.WhenToUse,
		Variables: cloneAdapterJSON(row.Variables), Tags: cloneAdapterJSON(row.Tags), Enabled: row.Enabled,
		ManuallyEdited: row.ManuallyEdited, MatchWhen: cloneAdapterJSON(row.MatchWhen), Priority: row.Priority,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt, Description: row.Description,
	}
}

func threadPromptSectionToThread(row threadprompt.PromptTemplateSection) thread.PromptTemplateSection {
	return thread.PromptTemplateSection{
		ID: row.ID, TemplateID: row.TemplateID, SectionKey: row.SectionKey, Region: row.Region,
		Ordinal: row.Ordinal, Body: row.Body, EnableWhen: cloneAdapterJSON(row.EnableWhen), Enabled: row.Enabled,
		TriggerType: row.TriggerType, RecallTopic: row.RecallTopic, TemplatePromptKey: row.TemplatePromptKey,
		TemplateTitle: row.TemplateTitle, TemplateDescription: row.TemplateDescription,
		TemplateWhenToUse: row.TemplateWhenToUse, TemplateTags: cloneAdapterJSON(row.TemplateTags),
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
}

func threadPromptVersionFromThread(row thread.PromptTemplateVersion) threadprompt.PromptTemplateVersion {
	return threadprompt.PromptTemplateVersion{
		ID: row.ID, PromptKey: row.PromptKey, Title: row.Title, AgentKey: row.AgentKey,
		ToolName: row.ToolName, PromptText: row.PromptText, Variables: cloneAdapterJSON(row.Variables),
		Tags: cloneAdapterJSON(row.Tags), Description: row.Description, Enabled: row.Enabled,
		CreatedBy: row.CreatedBy, UpdatedBy: row.UpdatedBy, SourceUpdatedAt: row.SourceUpdatedAt,
		CreatedAt: row.CreatedAt, ArchivedAt: row.ArchivedAt,
	}
}
