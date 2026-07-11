package promptadapter

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/app/internal/storeguard"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	sharedfilestore "github.com/anthropic-ai/super-agent-v3/internal/store/sharedfile"
	"github.com/anthropic-ai/super-agent-v3/internal/store/uipreference"
)

var (
	errPromptPreferenceStoreNotConfigured = errors.New("prompt preference store is not configured")
	errPromptSharedFileStoreNotConfigured = errors.New("prompt shared file store is not configured")
)

type promptStoreAdapter struct {
	store promptstore.Store
	*promptTemplateStoreAdapter
	*promptSectionStoreAdapter
	*promptIntentDraftStoreAdapter
}

type promptTemplateStoreAdapter struct{ store promptstore.Store }
type promptSectionStoreAdapter struct{ store promptstore.Store }
type promptIntentDraftStoreAdapter struct{ store promptstore.Store }

type promptPreferenceReaderAdapter struct {
	store uipreference.Store
}

type promptSharedFileReaderAdapter struct {
	store sharedfilestore.Reader
}

var (
	_ prompt.Store            = (*promptStoreAdapter)(nil)
	_ prompt.PreferenceReader = (*promptPreferenceReaderAdapter)(nil)
	_ prompt.SharedFileReader = (*promptSharedFileReaderAdapter)(nil)
)

// providePromptStore 在 App 组合边界把 concrete Store 收窄为 prompt 领域端口。
func providePromptStore(store promptstore.Store) (prompt.Store, error) {
	if storeguard.IsNil(store) {
		return nil, prompt.ErrStoreNotConfigured
	}
	return newPromptStoreAdapter(store), nil
}

// newPromptStoreAdapter 按模板、section 与 intent draft 能力拆分方法所有权。
func newPromptStoreAdapter(store promptstore.Store) *promptStoreAdapter {
	return &promptStoreAdapter{
		store:                         store,
		promptTemplateStoreAdapter:    &promptTemplateStoreAdapter{store: store},
		promptSectionStoreAdapter:     &promptSectionStoreAdapter{store: store},
		promptIntentDraftStoreAdapter: &promptIntentDraftStoreAdapter{store: store},
	}
}

// providePromptPreferenceReader 把 UI preference Store 收窄为 prompt 的只读端口。
func providePromptPreferenceReader(store uipreference.Store) (prompt.PreferenceReader, error) {
	if storeguard.IsNil(store) {
		return nil, errPromptPreferenceStoreNotConfigured
	}
	return &promptPreferenceReaderAdapter{store: store}, nil
}

// providePromptSharedFileReader 把 shared file Store 收窄为 prompt 的内容读取端口。
func providePromptSharedFileReader(store sharedfilestore.Reader) (prompt.SharedFileReader, error) {
	if storeguard.IsNil(store) {
		return nil, errPromptSharedFileStoreNotConfigured
	}
	return &promptSharedFileReaderAdapter{store: store}, nil
}

// WithTx 保持底层事务 Store 身份，并拒绝缺失 callback 或事务 Store。
func (a *promptStoreAdapter) WithTx(ctx context.Context, fn func(prompt.Store) error) error {
	if fn == nil {
		return prompt.ErrStoreTxCallbackRequired
	}
	if a == nil || storeguard.IsNil(a.store) {
		return prompt.ErrStoreNotConfigured
	}
	return a.store.WithTx(ctx, func(txStore promptstore.Store) error {
		if storeguard.IsNil(txStore) {
			return prompt.ErrStoreNotConfigured
		}
		return fn(newPromptStoreAdapter(txStore))
	})
}

// List 查询模板并把 Store DTO 复制为 prompt 领域 DTO。
func (a *promptTemplateStoreAdapter) List(ctx context.Context, filter prompt.ListFilter) ([]prompt.Template, error) {
	rows, err := a.store.List(ctx, toStorePromptListFilter(filter))
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplates(rows), nil
}

// Get 读取模板；Store 的 nil,nil 结果保持不变。
func (a *promptTemplateStoreAdapter) Get(ctx context.Context, promptKey string) (*prompt.Template, error) {
	row, err := a.store.Get(ctx, promptKey)
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplatePtr(row), nil
}

// Delete 删除模板并原样保留 Store 错误身份。
func (a *promptTemplateStoreAdapter) Delete(ctx context.Context, promptKey string) error {
	return a.store.Delete(ctx, promptKey)
}

// InsertVersion 逐字段转换版本快照后归档。
func (a *promptTemplateStoreAdapter) InsertVersion(ctx context.Context, version prompt.TemplateVersion) (int64, error) {
	return a.store.InsertVersion(ctx, toStorePromptTemplateVersion(version))
}

// CreatePromptTemplate 创建模板并把结果复制回领域 DTO。
func (a *promptTemplateStoreAdapter) CreatePromptTemplate(
	ctx context.Context,
	template prompt.Template,
) (*prompt.Template, error) {
	row, err := a.store.CreatePromptTemplate(ctx, toStorePromptTemplate(template))
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplatePtr(row), nil
}

// Upsert 写入模板并把结果复制回领域 DTO。
func (a *promptTemplateStoreAdapter) Upsert(ctx context.Context, template prompt.Template) (*prompt.Template, error) {
	row, err := a.store.Upsert(ctx, toStorePromptTemplate(template))
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplatePtr(row), nil
}

// ListSectionsByTemplateID 查询单模板 sections，并返回独立切片。
func (a *promptSectionStoreAdapter) ListSectionsByTemplateID(
	ctx context.Context,
	templateID int64,
) ([]prompt.TemplateSection, error) {
	rows, err := a.store.ListSectionsByTemplateID(ctx, templateID)
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplateSections(rows), nil
}

// ListSectionsByTemplateIDs 复制批量 ID 输入和 section 输出。
func (a *promptSectionStoreAdapter) ListSectionsByTemplateIDs(
	ctx context.Context,
	templateIDs []int64,
) ([]prompt.TemplateSection, error) {
	ids := append([]int64(nil), templateIDs...)
	rows, err := a.store.ListSectionsByTemplateIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplateSections(rows), nil
}

// ListRecallSections 查询 cwd 内 recall sections，并返回独立切片。
func (a *promptSectionStoreAdapter) ListRecallSections(ctx context.Context, cwd string) ([]prompt.TemplateSection, error) {
	rows, err := a.store.ListRecallSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplateSections(rows), nil
}

// ListDefaultRuleSections 查询 cwd 内默认规则 sections，并返回独立切片。
func (a *promptSectionStoreAdapter) ListDefaultRuleSections(ctx context.Context, cwd string) ([]prompt.TemplateSection, error) {
	rows, err := a.store.ListDefaultRuleSections(ctx, cwd)
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplateSections(rows), nil
}

// UpsertSection 写入 section 并复制返回结果。
func (a *promptSectionStoreAdapter) UpsertSection(
	ctx context.Context,
	section prompt.TemplateSection,
) (*prompt.TemplateSection, error) {
	row, err := a.store.UpsertSection(ctx, toStorePromptTemplateSection(section))
	if err != nil {
		return nil, err
	}
	return fromStorePromptTemplateSectionPtr(row), nil
}

// DeleteSection 删除指定 section。
func (a *promptSectionStoreAdapter) DeleteSection(ctx context.Context, templateID int64, sectionKey string) error {
	return a.store.DeleteSection(ctx, templateID, sectionKey)
}

// UpsertRecallTopicTargetInCWD 维护 cwd 内 recall topic 索引。
func (a *promptSectionStoreAdapter) UpsertRecallTopicTargetInCWD(
	ctx context.Context,
	cwd, topic string,
	templateID int64,
	sectionKey string,
) error {
	return a.store.UpsertRecallTopicTargetInCWD(ctx, cwd, topic, templateID, sectionKey)
}

// UpsertIntentDraft 写入 intent 草稿并复制返回结果。
func (a *promptIntentDraftStoreAdapter) UpsertIntentDraft(
	ctx context.Context,
	draft prompt.IntentDraft,
) (*prompt.IntentDraft, error) {
	row, err := a.store.UpsertIntentDraft(ctx, toStorePromptIntentDraft(draft))
	if err != nil {
		return nil, err
	}
	return fromStorePromptIntentDraftPtr(row), nil
}

// GetIntentDraft 读取 intent 草稿；Store 的 nil,nil 结果保持不变。
func (a *promptIntentDraftStoreAdapter) GetIntentDraft(ctx context.Context, cwd, draftKey string) (*prompt.IntentDraft, error) {
	row, err := a.store.GetIntentDraft(ctx, cwd, draftKey)
	if err != nil {
		return nil, err
	}
	return fromStorePromptIntentDraftPtr(row), nil
}

// ListIntentDrafts 查询 intent 草稿并返回独立切片。
func (a *promptIntentDraftStoreAdapter) ListIntentDrafts(
	ctx context.Context,
	filter prompt.IntentDraftListFilter,
) ([]prompt.IntentDraft, error) {
	rows, err := a.store.ListIntentDrafts(ctx, toStorePromptIntentDraftListFilter(filter))
	if err != nil {
		return nil, err
	}
	return fromStorePromptIntentDrafts(rows), nil
}

// UpdateIntentDraftStatus 更新草稿状态并复制返回结果。
func (a *promptIntentDraftStoreAdapter) UpdateIntentDraftStatus(
	ctx context.Context,
	cwd, draftKey, status string,
) (*prompt.IntentDraft, error) {
	row, err := a.store.UpdateIntentDraftStatus(ctx, cwd, draftKey, status)
	if err != nil {
		return nil, err
	}
	return fromStorePromptIntentDraftPtr(row), nil
}

// LockRecallTopicInCWD 锁定 cwd 内 recall topic。
func (a *promptSectionStoreAdapter) LockRecallTopicInCWD(ctx context.Context, cwd, topic string) error {
	return a.store.LockRecallTopicInCWD(ctx, cwd, topic)
}

// GetValue 读取偏好并复制 RawMessage，避免领域层与 Store 共享 backing array。
func (a *promptPreferenceReaderAdapter) GetValue(ctx context.Context, cwd, key string) (json.RawMessage, error) {
	value, err := a.store.GetValue(ctx, cwd, key)
	if err != nil {
		return nil, err
	}
	return clonePromptJSON(value), nil
}

// GetContent 只向领域暴露内容；成功的 nil 文件保持为空字符串语义。
func (a *promptSharedFileReaderAdapter) GetContent(ctx context.Context, path string) (string, error) {
	file, err := a.store.Get(ctx, path)
	if err != nil {
		return "", err
	}
	if file == nil {
		return "", nil
	}
	return file.Content, nil
}

func toStorePromptListFilter(filter prompt.ListFilter) promptstore.ListFilter {
	return promptstore.ListFilter{AgentKey: filter.AgentKey, Keyword: filter.Keyword, CWD: filter.CWD, Limit: filter.Limit}
}

func toStorePromptTemplate(value prompt.Template) promptstore.PromptTemplate {
	return promptstore.PromptTemplate{
		ID: value.ID, PromptKey: value.PromptKey, Title: value.Title, AgentKey: value.AgentKey,
		ToolName: value.ToolName, PromptText: value.PromptText, WhenToUse: value.WhenToUse,
		Variables: clonePromptJSON(value.Variables), Tags: clonePromptJSON(value.Tags), Enabled: value.Enabled,
		ManuallyEdited: value.ManuallyEdited, MatchWhen: clonePromptJSON(value.MatchWhen), Priority: value.Priority,
		CreatedBy: value.CreatedBy, UpdatedBy: value.UpdatedBy, CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt, Description: value.Description,
	}
}

func fromStorePromptTemplate(value promptstore.PromptTemplate) prompt.Template {
	return prompt.Template{
		ID: value.ID, PromptKey: value.PromptKey, Title: value.Title, AgentKey: value.AgentKey,
		ToolName: value.ToolName, PromptText: value.PromptText, WhenToUse: value.WhenToUse,
		Variables: clonePromptJSON(value.Variables), Tags: clonePromptJSON(value.Tags), Enabled: value.Enabled,
		ManuallyEdited: value.ManuallyEdited, MatchWhen: clonePromptJSON(value.MatchWhen), Priority: value.Priority,
		CreatedBy: value.CreatedBy, UpdatedBy: value.UpdatedBy, CreatedAt: value.CreatedAt,
		UpdatedAt: value.UpdatedAt, Description: value.Description,
	}
}

func toStorePromptTemplateSection(value prompt.TemplateSection) promptstore.PromptTemplateSection {
	return promptstore.PromptTemplateSection{
		ID: value.ID, TemplateID: value.TemplateID, SectionKey: value.SectionKey, Region: value.Region,
		Ordinal: value.Ordinal, Body: value.Body, EnableWhen: clonePromptJSON(value.EnableWhen), Enabled: value.Enabled,
		TriggerType: value.TriggerType, RecallTopic: value.RecallTopic, TemplatePromptKey: value.TemplatePromptKey,
		TemplateTitle: value.TemplateTitle, TemplateDescription: value.TemplateDescription,
		TemplateWhenToUse: value.TemplateWhenToUse, TemplateTags: clonePromptJSON(value.TemplateTags),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func fromStorePromptTemplateSection(value promptstore.PromptTemplateSection) prompt.TemplateSection {
	return prompt.TemplateSection{
		ID: value.ID, TemplateID: value.TemplateID, SectionKey: value.SectionKey, Region: value.Region,
		Ordinal: value.Ordinal, Body: value.Body, EnableWhen: clonePromptJSON(value.EnableWhen), Enabled: value.Enabled,
		TriggerType: value.TriggerType, RecallTopic: value.RecallTopic, TemplatePromptKey: value.TemplatePromptKey,
		TemplateTitle: value.TemplateTitle, TemplateDescription: value.TemplateDescription,
		TemplateWhenToUse: value.TemplateWhenToUse, TemplateTags: clonePromptJSON(value.TemplateTags),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func toStorePromptTemplateVersion(value prompt.TemplateVersion) promptstore.PromptTemplateVersion {
	return promptstore.PromptTemplateVersion{
		ID: value.ID, PromptKey: value.PromptKey, Title: value.Title, AgentKey: value.AgentKey,
		ToolName: value.ToolName, PromptText: value.PromptText, Variables: clonePromptJSON(value.Variables),
		Tags: clonePromptJSON(value.Tags), Description: value.Description, Enabled: value.Enabled,
		CreatedBy: value.CreatedBy, UpdatedBy: value.UpdatedBy, SourceUpdatedAt: clonePromptTime(value.SourceUpdatedAt),
		CreatedAt: value.CreatedAt, ArchivedAt: value.ArchivedAt,
	}
}

func fromStorePromptTemplateVersion(value promptstore.PromptTemplateVersion) prompt.TemplateVersion {
	return prompt.TemplateVersion{
		ID: value.ID, PromptKey: value.PromptKey, Title: value.Title, AgentKey: value.AgentKey,
		ToolName: value.ToolName, PromptText: value.PromptText, Variables: clonePromptJSON(value.Variables),
		Tags: clonePromptJSON(value.Tags), Description: value.Description, Enabled: value.Enabled,
		CreatedBy: value.CreatedBy, UpdatedBy: value.UpdatedBy, SourceUpdatedAt: clonePromptTime(value.SourceUpdatedAt),
		CreatedAt: value.CreatedAt, ArchivedAt: value.ArchivedAt,
	}
}

func toStorePromptIntentDraft(value prompt.IntentDraft) promptstore.PromptIntentDraft {
	return promptstore.PromptIntentDraft{
		ID: value.ID, DraftKey: value.DraftKey, CWD: value.CWD, Kind: value.Kind, RawInput: value.RawInput,
		SourceType: value.SourceType, SourceURL: value.SourceURL, OriginHash: value.OriginHash,
		LicenseHint: value.LicenseHint, GeneratedCard: clonePromptJSON(value.GeneratedCard), Confidence: value.Confidence,
		Status: value.Status, Scope: value.Scope, Issues: clonePromptJSON(value.Issues),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func fromStorePromptIntentDraft(value promptstore.PromptIntentDraft) prompt.IntentDraft {
	return prompt.IntentDraft{
		ID: value.ID, DraftKey: value.DraftKey, CWD: value.CWD, Kind: value.Kind, RawInput: value.RawInput,
		SourceType: value.SourceType, SourceURL: value.SourceURL, OriginHash: value.OriginHash,
		LicenseHint: value.LicenseHint, GeneratedCard: clonePromptJSON(value.GeneratedCard), Confidence: value.Confidence,
		Status: value.Status, Scope: value.Scope, Issues: clonePromptJSON(value.Issues),
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func toStorePromptIntentDraftListFilter(filter prompt.IntentDraftListFilter) promptstore.PromptIntentDraftListFilter {
	return promptstore.PromptIntentDraftListFilter{CWD: filter.CWD, Status: filter.Status, Limit: filter.Limit}
}

func fromStorePromptTemplatePtr(value *promptstore.PromptTemplate) *prompt.Template {
	if value == nil {
		return nil
	}
	mapped := fromStorePromptTemplate(*value)
	return &mapped
}

func fromStorePromptTemplateSectionPtr(value *promptstore.PromptTemplateSection) *prompt.TemplateSection {
	if value == nil {
		return nil
	}
	mapped := fromStorePromptTemplateSection(*value)
	return &mapped
}

func fromStorePromptIntentDraftPtr(value *promptstore.PromptIntentDraft) *prompt.IntentDraft {
	if value == nil {
		return nil
	}
	mapped := fromStorePromptIntentDraft(*value)
	return &mapped
}

func fromStorePromptTemplates(values []promptstore.PromptTemplate) []prompt.Template {
	result := make([]prompt.Template, len(values))
	for index, value := range values {
		result[index] = fromStorePromptTemplate(value)
	}
	return result
}

func fromStorePromptTemplateSections(values []promptstore.PromptTemplateSection) []prompt.TemplateSection {
	result := make([]prompt.TemplateSection, len(values))
	for index, value := range values {
		result[index] = fromStorePromptTemplateSection(value)
	}
	return result
}

func fromStorePromptIntentDrafts(values []promptstore.PromptIntentDraft) []prompt.IntentDraft {
	result := make([]prompt.IntentDraft, len(values))
	for index, value := range values {
		result[index] = fromStorePromptIntentDraft(value)
	}
	return result
}

func clonePromptJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func clonePromptTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyValue := *value
	return &copyValue
}
