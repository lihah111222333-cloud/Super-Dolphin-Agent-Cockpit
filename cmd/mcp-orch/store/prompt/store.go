package prompt

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared/builtinprompts"
)

// store 用 sqlc 查询实现 prompt Store 接口，并保留事务复用所需的 DBTX。
type store struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

// NewStore 创建 prompt template 存储。
func NewStore(db sqlc.DBTX) Store { return &store{db: db, q: sqlc.New(db)} }

// Get 按 prompt key 读取单个 prompt template。
func (s *store) Get(ctx context.Context, promptKey string) (*PromptTemplate, error) {
	row, err := s.q.GetPromptTemplate(ctx, sqlc.GetPromptTemplateParams{PromptKey: promptKey})
	if err != nil {
		return nil, wrapPromptError(err, "get", "prompt_template")
	}
	mapped := fromGetTemplate(row)
	return &mapped, nil
}

// GetSectionByRecallTopic 先按 cwd 读取用户模板 section，找不到时回退到内置全局模板。
func (s *store) GetSectionByRecallTopic(ctx context.Context, cwd, topic string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	topic = strings.TrimSpace(topic)
	if cwd == "" {
		return "", fmt.Errorf("cwd is required for prompt recall")
	}
	if topic == "" {
		return "", fmt.Errorf("topic is required for prompt recall")
	}
	body, err := s.q.GetPromptRecallSectionBody(ctx, sqlc.GetPromptRecallSectionBodyParams{RecallTopic: topic, CWD: &cwd})
	if err == nil {
		return body, nil
	}
	wrapped := wrapPromptError(err, "get_section", "prompt_template_section")
	if !platformdb.IsNotFound(wrapped) {
		return "", wrapped
	}
	body, ok, builtinErr := builtinRecallSectionBody(topic)
	if builtinErr != nil {
		return "", fmt.Errorf("get_section builtin prompt registry: %w", builtinErr)
	}
	if ok {
		return body, nil
	}
	return "", wrapped
}

// builtinRecallSectionBody 从内置 prompt registry 中查找 recall topic 对应正文。
func builtinRecallSectionBody(topic string) (string, bool, error) {
	registry, err := builtinprompts.NewDefaultRegistry()
	if err != nil {
		return "", false, err
	}
	for _, template := range registry.ListTemplates() {
		if !template.Enabled || !builtinTemplateScopeVisibleForRecall(template.Scope) {
			continue
		}
		for _, section := range registry.SectionsByTemplateID(template.ID) {
			if section.Enabled &&
				strings.TrimSpace(section.TriggerType) == "recall" &&
				strings.TrimSpace(section.RecallTopic) == topic &&
				strings.TrimSpace(section.Body) != "" {
				return section.Body, true, nil
			}
		}
	}
	return "", false, nil
}

// builtinTemplateScopeVisibleForRecall 判断内置模板 scope 是否可用于 recall fallback。
func builtinTemplateScopeVisibleForRecall(scope string) bool {
	scope = strings.TrimSpace(scope)
	return scope == "" || scope == "global"
}

// ListSectionsByTemplateID 按 template id 列出全部 prompt sections。
func (s *store) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error) {
	rows, err := s.q.ListPromptTemplateSectionsByTemplate(ctx, sqlc.ListPromptTemplateSectionsByTemplateParams{TemplateID: templateID})
	if err != nil {
		return nil, wrapPromptError(err, "list_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromSectionRow(row))
	}
	return sections, nil
}

// List 列出 prompt templates；runtime-visible 查询必须携带 cwd 以应用作用域过滤。
func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	cwd := strings.TrimSpace(filter.CWD)
	if filter.RuntimeVisible && cwd == "" {
		return nil, fmt.Errorf("cwd is required for runtime-visible prompt list")
	}
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		AgentKey:       filter.AgentKey,
		Keyword:        filter.Keyword,
		RuntimeVisible: boolInt64(filter.RuntimeVisible),
		CWD:            &cwd,
		LimitCount:     int64(filter.Limit),
	})
	if err != nil {
		return nil, wrapPromptError(err, "list", "prompt_template")
	}
	templates := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, fromListTemplate(row))
	}
	return templates, nil
}

// WithTx 在同一事务内执行多步 prompt store 操作。
func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	return wrapPromptError(sqlctx.WithTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_tx", "prompt_template")
}

// Delete 按 prompt key 删除 prompt template。
func (s *store) Delete(ctx context.Context, promptKey string) error {
	_, err := s.q.DeletePromptTemplate(ctx, sqlc.DeletePromptTemplateParams{PromptKey: promptKey})
	return wrapPromptError(err, "delete", "prompt_template")
}

// InsertVersion 插入 prompt template 历史版本快照并返回版本 id。
func (s *store) InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error) {
	id, err := s.q.InsertPromptVersion(ctx, sqlc.InsertPromptVersionParams{
		PromptKey:       version.PromptKey,
		Title:           version.Title,
		AgentKey:        version.AgentKey,
		ToolName:        version.ToolName,
		PromptText:      version.PromptText,
		Variables:       version.Variables,
		Tags:            version.Tags,
		Description:     version.Description,
		Enabled:         boolInt64(version.Enabled),
		CreatedBy:       version.CreatedBy,
		UpdatedBy:       version.UpdatedBy,
		SourceUpdatedAt: sqlc.TimeValuePtr(version.SourceUpdatedAt),
	})
	if err != nil {
		return 0, wrapPromptError(err, "insert_version", "prompt_template_version")
	}
	return id, nil
}

// Upsert 新增或更新 prompt template 当前版本。
func (s *store) Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	row, err := s.q.UpsertPromptTemplate(ctx, sqlc.UpsertPromptTemplateParams{
		PromptKey:      template.PromptKey,
		Title:          template.Title,
		AgentKey:       template.AgentKey,
		ToolName:       template.ToolName,
		PromptText:     template.PromptText,
		Variables:      template.Variables,
		Tags:           template.Tags,
		Description:    template.Description,
		WhenToUse:      template.WhenToUse,
		Enabled:        boolInt64(template.Enabled),
		ManuallyEdited: boolInt64(template.ManuallyEdited),
		MatchWhen:      template.MatchWhen,
		Priority:       int64(template.Priority),
		CreatedBy:      template.CreatedBy,
		UpdatedBy:      template.UpdatedBy,
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_template")
	}
	mapped := fromUpsertTemplate(row)
	return &mapped, nil
}

// fromGetTemplate 将详情查询行映射为 PromptTemplate。
func fromGetTemplate(row sqlc.GetPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		Variables:      json.RawMessage(row.Variables),
		Tags:           json.RawMessage(row.Tags),
		Enabled:        int64Bool(row.Enabled),
		ManuallyEdited: int64Bool(row.ManuallyEdited),
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:      sqlc.TimeValue(row.UpdatedAt),
		Description:    row.Description,
		WhenToUse:      row.WhenToUse,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       int32(row.Priority),
	}
}

// fromListTemplate 将列表查询行映射为 PromptTemplate。
func fromListTemplate(row sqlc.ListPromptTemplatesRow) PromptTemplate {
	return PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		Variables:      json.RawMessage(row.Variables),
		Tags:           json.RawMessage(row.Tags),
		Enabled:        int64Bool(row.Enabled),
		ManuallyEdited: int64Bool(row.ManuallyEdited),
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:      sqlc.TimeValue(row.UpdatedAt),
		Description:    row.Description,
		WhenToUse:      row.WhenToUse,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       int32(row.Priority),
	}
}

// fromUpsertTemplate 将 upsert 返回行映射为 PromptTemplate。
func fromUpsertTemplate(row sqlc.UpsertPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:             row.ID,
		PromptKey:      row.PromptKey,
		Title:          row.Title,
		AgentKey:       row.AgentKey,
		ToolName:       row.ToolName,
		PromptText:     row.PromptText,
		Variables:      json.RawMessage(row.Variables),
		Tags:           json.RawMessage(row.Tags),
		Enabled:        int64Bool(row.Enabled),
		ManuallyEdited: int64Bool(row.ManuallyEdited),
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:      sqlc.TimeValue(row.UpdatedAt),
		Description:    row.Description,
		WhenToUse:      row.WhenToUse,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       int32(row.Priority),
	}
}

// fromSectionRow 将 section 查询行映射为 PromptTemplateSection。
func fromSectionRow(row sqlc.ListPromptTemplateSectionsByTemplateRow) PromptTemplateSection {
	return PromptTemplateSection{
		ID:          row.ID,
		TemplateID:  row.TemplateID,
		SectionKey:  row.SectionKey,
		Region:      row.Region,
		Ordinal:     int32(row.Ordinal),
		Body:        row.Body,
		TriggerType: row.TriggerType,
		RecallTopic: row.RecallTopic,
		Enabled:     int64Bool(row.Enabled),
	}
}

// boolInt64 将 bool 转为 SQLite 使用的 0/1。
func boolInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

// int64Bool 将 SQLite 0/1 转为 bool。
func int64Bool(value int64) bool {
	return value != 0
}

// wrapPromptError 统一 prompt store 错误域。
func wrapPromptError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
