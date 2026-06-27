package threadprompt

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
)

// NewRuntimeCatalog 对外保留 thread 模块需要的 promptstore catalog 接口，
// 内部先转换为 threadprompt 本地 DTO/port，再由 adapter 转回既有 thread 路由接口。
func NewRuntimeCatalog(store promptstore.Store, builtin contract.BuiltinPromptRegistry) promptstore.RuntimePromptCatalog {
	catalog := newRuntimeCatalogForStore(store, builtin)
	if catalog == nil {
		return nil
	}
	return promptRuntimeCatalogAdapter{catalog: catalog}
}

func newRuntimeCatalogForStore(store promptstore.Store, builtin contract.BuiltinPromptRegistry) RuntimePromptCatalog {
	return newRuntimeCatalog(newPromptStoreAdapter(store), builtin)
}

// RegisterProviders 是 thread 模块的 assembly 入口，把既有 promptstore catalog 适配为本包运行时 catalog。
func RegisterProviders(registrar contract.DynamicSectionRegistrar, catalog promptstore.RuntimePromptCatalog) error {
	return registerProviders(registrar, adaptRuntimePromptCatalog(catalog))
}

type localRuntimeCatalogProvider interface {
	localRuntimePromptCatalog() RuntimePromptCatalog
}

func adaptRuntimePromptCatalog(catalog promptstore.RuntimePromptCatalog) RuntimePromptCatalog {
	if catalog == nil {
		return nil
	}
	if provider, ok := catalog.(localRuntimeCatalogProvider); ok {
		return provider.localRuntimePromptCatalog()
	}
	return promptRuntimeCatalogLocalAdapter{catalog: catalog}
}

type promptStoreAdapter struct {
	store promptstore.Store
}

func newPromptStoreAdapter(store promptstore.Store) PromptStore {
	if store == nil {
		return nil
	}
	return promptStoreAdapter{store: store}
}

// List 将 store 列表查询结果转换成本包模板 DTO。
func (a promptStoreAdapter) List(ctx context.Context, filter promptListFilter) ([]PromptTemplate, error) {
	rows, err := a.store.List(ctx, promptstore.ListFilter{
		AgentKey: filter.AgentKey,
		Keyword:  filter.Keyword,
		CWD:      filter.CWD,
		Limit:    filter.Limit,
	})
	if err != nil {
		return nil, err
	}
	return promptTemplatesFromStore(rows), nil
}

// Get 按 prompt_key 读取模板并转换成本包 DTO。
func (a promptStoreAdapter) Get(ctx context.Context, promptKey string) (*PromptTemplate, error) {
	row, err := a.store.Get(ctx, promptKey)
	if err != nil || row == nil {
		return nil, err
	}
	return promptTemplateFromStore(*row), nil
}

// InsertVersion 将本包版本 DTO 转换后委托给 prompt store 写入归档。
func (a promptStoreAdapter) InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error) {
	return a.store.InsertVersion(ctx, promptTemplateVersionToStore(version))
}

// ListSectionsByTemplateID 读取单模板 sections 并转换成本包 section DTO。
func (a promptStoreAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsFromStore(rows), nil
}

// ListSectionsByTemplateIDs 批量读取模板 sections 并转换成本包 section DTO。
func (a promptStoreAdapter) ListSectionsByTemplateIDs(ctx context.Context, templateIDs []int64) ([]PromptTemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateIDs(ctx, templateIDs)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsFromStore(rows), nil
}

// ListRecallSections 读取 recall sections 并转换成本包 section DTO。
func (a promptStoreAdapter) ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	rows, err := a.store.ListRecallSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsFromStore(rows), nil
}

// ListDefaultRuleSections 读取默认规则 sections 并转换成本包 section DTO。
func (a promptStoreAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	rows, err := a.store.ListDefaultRuleSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsFromStore(rows), nil
}

type promptRuntimeCatalogAdapter struct {
	catalog RuntimePromptCatalog
}

func (a promptRuntimeCatalogAdapter) localRuntimePromptCatalog() RuntimePromptCatalog {
	return a.catalog
}

// ListTemplates 将本包 catalog 结果转换为 thread 现有路由接口需要的 store DTO。
func (a promptRuntimeCatalogAdapter) ListTemplates(ctx context.Context, filter promptstore.RuntimeListFilter) ([]promptstore.PromptTemplate, error) {
	rows, err := a.catalog.ListTemplates(ctx, runtimeListFilterFromStore(filter))
	if err != nil {
		return nil, err
	}
	return promptTemplatesToStore(rows), nil
}

// GetTemplate 将本包模板 DTO 转换为 thread 现有路由接口需要的 store DTO。
func (a promptRuntimeCatalogAdapter) GetTemplate(ctx context.Context, promptKey, cwd string) (*promptstore.PromptTemplate, error) {
	row, err := a.catalog.GetTemplate(ctx, promptKey, cwd)
	if err != nil || row == nil {
		return nil, err
	}
	return promptTemplateToStore(*row), nil
}

// ListSectionsByTemplateID 将本包 section DTO 转换为 thread 现有路由接口需要的 store DTO。
func (a promptRuntimeCatalogAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]promptstore.PromptTemplateSection, error) {
	rows, err := a.catalog.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsToStore(rows), nil
}

// ListRecallSections 将本包 recall section DTO 转换为 thread 现有路由接口需要的 store DTO。
func (a promptRuntimeCatalogAdapter) ListRecallSections(ctx context.Context, cwd string) ([]promptstore.PromptTemplateSection, error) {
	rows, err := a.catalog.ListRecallSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsToStore(rows), nil
}

// ListDefaultRuleSections 将本包默认规则 section DTO 转换为 thread 现有路由接口需要的 store DTO。
func (a promptRuntimeCatalogAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]promptstore.PromptTemplateSection, error) {
	rows, err := a.catalog.ListDefaultRuleSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsToStore(rows), nil
}

// InsertVersion 将 thread 传入的 store 版本 DTO 转换成本包 DTO 后写入 catalog。
func (a promptRuntimeCatalogAdapter) InsertVersion(ctx context.Context, version promptstore.PromptTemplateVersion) (int64, error) {
	return a.catalog.InsertVersion(ctx, promptTemplateVersionFromStore(version))
}

// CanInsertPromptVersion 透传本包 catalog 的写入能力标记，缺省保持可写以兼容旧接口。
func (a promptRuntimeCatalogAdapter) CanInsertPromptVersion() bool {
	checker, ok := a.catalog.(interface{ CanInsertPromptVersion() bool })
	return !ok || checker.CanInsertPromptVersion()
}

type promptRuntimeCatalogLocalAdapter struct {
	catalog promptstore.RuntimePromptCatalog
}

// ListTemplates 将外部 promptstore catalog 结果转换成本包模板 DTO。
func (a promptRuntimeCatalogLocalAdapter) ListTemplates(ctx context.Context, filter RuntimeListFilter) ([]PromptTemplate, error) {
	rows, err := a.catalog.ListTemplates(ctx, runtimeListFilterToStore(filter))
	if err != nil {
		return nil, err
	}
	return promptTemplatesFromStore(rows), nil
}

// GetTemplate 将外部 promptstore catalog 的单模板结果转换成本包 DTO。
func (a promptRuntimeCatalogLocalAdapter) GetTemplate(ctx context.Context, promptKey, cwd string) (*PromptTemplate, error) {
	row, err := a.catalog.GetTemplate(ctx, promptKey, cwd)
	if err != nil || row == nil {
		return nil, err
	}
	return promptTemplateFromStore(*row), nil
}

// ListSectionsByTemplateID 将外部 catalog 的 section 结果转换成本包 DTO。
func (a promptRuntimeCatalogLocalAdapter) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error) {
	rows, err := a.catalog.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsFromStore(rows), nil
}

// ListRecallSections 将外部 catalog 的 recall section 结果转换成本包 DTO。
func (a promptRuntimeCatalogLocalAdapter) ListRecallSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	rows, err := a.catalog.ListRecallSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsFromStore(rows), nil
}

// ListDefaultRuleSections 将外部 catalog 的默认规则 section 结果转换成本包 DTO。
func (a promptRuntimeCatalogLocalAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]PromptTemplateSection, error) {
	rows, err := a.catalog.ListDefaultRuleSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return promptTemplateSectionsFromStore(rows), nil
}

// InsertVersion 将本包版本 DTO 转换后写入外部 promptstore catalog。
func (a promptRuntimeCatalogLocalAdapter) InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error) {
	return a.catalog.InsertVersion(ctx, promptTemplateVersionToStore(version))
}

func runtimeListFilterFromStore(filter promptstore.RuntimeListFilter) RuntimeListFilter {
	return RuntimeListFilter{
		AgentKey: filter.AgentKey,
		Keyword:  filter.Keyword,
		CWD:      filter.CWD,
		Limit:    filter.Limit,
	}
}

func runtimeListFilterToStore(filter RuntimeListFilter) promptstore.RuntimeListFilter {
	return promptstore.RuntimeListFilter{
		AgentKey: filter.AgentKey,
		Keyword:  filter.Keyword,
		CWD:      filter.CWD,
		Limit:    filter.Limit,
	}
}

func promptTemplatesFromStore(rows []promptstore.PromptTemplate) []PromptTemplate {
	out := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, *promptTemplateFromStore(row))
	}
	return out
}

func promptTemplateFromStore(row promptstore.PromptTemplate) *PromptTemplate {
	return &PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		WhenToUse:      row.WhenToUse,
		Variables:      cloneRuntimeRawJSON(row.Variables),
		Tags:           cloneRuntimeRawJSON(row.Tags),
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		MatchWhen:      cloneRuntimeRawJSON(row.MatchWhen),
		Priority:       row.Priority,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Description:    row.Description,
	}
}

func promptTemplatesToStore(rows []PromptTemplate) []promptstore.PromptTemplate {
	out := make([]promptstore.PromptTemplate, 0, len(rows))
	for _, row := range rows {
		out = append(out, *promptTemplateToStore(row))
	}
	return out
}

func promptTemplateToStore(row PromptTemplate) *promptstore.PromptTemplate {
	return &promptstore.PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		WhenToUse:      row.WhenToUse,
		Variables:      cloneRuntimeRawJSON(row.Variables),
		Tags:           cloneRuntimeRawJSON(row.Tags),
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		MatchWhen:      cloneRuntimeRawJSON(row.MatchWhen),
		Priority:       row.Priority,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      row.CreatedAt,
		UpdatedAt:      row.UpdatedAt,
		Description:    row.Description,
	}
}

func promptTemplateSectionsFromStore(rows []promptstore.PromptTemplateSection) []PromptTemplateSection {
	out := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptTemplateSectionFromStore(row))
	}
	return out
}

func promptTemplateSectionFromStore(row promptstore.PromptTemplateSection) PromptTemplateSection {
	return PromptTemplateSection{
		ID:                  row.ID,
		TemplateID:          row.TemplateID,
		SectionKey:          row.SectionKey,
		Region:              row.Region,
		Ordinal:             row.Ordinal,
		Body:                row.Body,
		EnableWhen:          cloneRuntimeRawJSON(row.EnableWhen),
		Enabled:             row.Enabled,
		TriggerType:         row.TriggerType,
		RecallTopic:         row.RecallTopic,
		TemplatePromptKey:   row.TemplatePromptKey,
		TemplateTitle:       row.TemplateTitle,
		TemplateDescription: row.TemplateDescription,
		TemplateWhenToUse:   row.TemplateWhenToUse,
		TemplateTags:        cloneRuntimeRawJSON(row.TemplateTags),
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

func promptTemplateSectionsToStore(rows []PromptTemplateSection) []promptstore.PromptTemplateSection {
	out := make([]promptstore.PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		out = append(out, promptTemplateSectionToStore(row))
	}
	return out
}

func promptTemplateSectionToStore(row PromptTemplateSection) promptstore.PromptTemplateSection {
	return promptstore.PromptTemplateSection{
		ID:                  row.ID,
		TemplateID:          row.TemplateID,
		SectionKey:          row.SectionKey,
		Region:              row.Region,
		Ordinal:             row.Ordinal,
		Body:                row.Body,
		EnableWhen:          cloneRuntimeRawJSON(row.EnableWhen),
		Enabled:             row.Enabled,
		TriggerType:         row.TriggerType,
		RecallTopic:         row.RecallTopic,
		TemplatePromptKey:   row.TemplatePromptKey,
		TemplateTitle:       row.TemplateTitle,
		TemplateDescription: row.TemplateDescription,
		TemplateWhenToUse:   row.TemplateWhenToUse,
		TemplateTags:        cloneRuntimeRawJSON(row.TemplateTags),
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
}

func promptTemplateVersionFromStore(row promptstore.PromptTemplateVersion) PromptTemplateVersion {
	return PromptTemplateVersion{
		ID:              row.ID,
		PromptKey:       row.PromptKey,
		Title:           row.Title,
		AgentKey:        row.AgentKey,
		ToolName:        row.ToolName,
		PromptText:      row.PromptText,
		Variables:       cloneRuntimeRawJSON(row.Variables),
		Tags:            cloneRuntimeRawJSON(row.Tags),
		Description:     row.Description,
		Enabled:         row.Enabled,
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		SourceUpdatedAt: row.SourceUpdatedAt,
		CreatedAt:       row.CreatedAt,
		ArchivedAt:      row.ArchivedAt,
	}
}

func promptTemplateVersionToStore(row PromptTemplateVersion) promptstore.PromptTemplateVersion {
	return promptstore.PromptTemplateVersion{
		ID:              row.ID,
		PromptKey:       row.PromptKey,
		Title:           row.Title,
		AgentKey:        row.AgentKey,
		ToolName:        row.ToolName,
		PromptText:      row.PromptText,
		Variables:       cloneRuntimeRawJSON(row.Variables),
		Tags:            cloneRuntimeRawJSON(row.Tags),
		Description:     row.Description,
		Enabled:         row.Enabled,
		CreatedBy:       row.CreatedBy,
		UpdatedBy:       row.UpdatedBy,
		SourceUpdatedAt: row.SourceUpdatedAt,
		CreatedAt:       row.CreatedAt,
		ArchivedAt:      row.ArchivedAt,
	}
}
