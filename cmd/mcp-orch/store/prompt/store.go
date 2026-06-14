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

type store struct {
	db sqlc.DBTX
	q  *sqlc.Queries
}

// NewStore 创建存储。
func NewStore(db sqlc.DBTX) Store { return &store{db: db, q: sqlc.New(db)} }

// Get 读取编排。
func (s *store) Get(ctx context.Context, promptKey string) (*PromptTemplate, error) {
	row, err := s.q.GetPromptTemplate(ctx, promptKey)
	if err != nil {
		return nil, wrapPromptError(err, "get", "prompt_template")
	}
	mapped := fromGetTemplate(row)
	return &mapped, nil
}

// GetSectionByRecallTopic 按recalltopic读取section。
func (s *store) GetSectionByRecallTopic(ctx context.Context, cwd, topic string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	topic = strings.TrimSpace(topic)
	if cwd == "" {
		return "", fmt.Errorf("cwd is required for prompt recall")
	}
	if topic == "" {
		return "", fmt.Errorf("topic is required for prompt recall")
	}
	body, err := s.q.GetPromptRecallSectionBody(ctx, sqlc.GetPromptRecallSectionBodyParams{RecallTopic: topic, Cwd: cwd})
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

// builtinRecallSectionBody 处理builtinrecallsection正文。
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

func builtinTemplateScopeVisibleForRecall(scope string) bool {
	scope = strings.TrimSpace(scope)
	return scope == "" || scope == "global"
}

// ListSectionsByTemplateID 按templateID列出sections。
func (s *store) ListSectionsByTemplateID(ctx context.Context, templateID int64) ([]PromptTemplateSection, error) {
	rows, err := s.q.ListPromptTemplateSectionsByTemplate(ctx, templateID)
	if err != nil {
		return nil, wrapPromptError(err, "list_sections", "prompt_template_sections")
	}
	sections := make([]PromptTemplateSection, 0, len(rows))
	for _, row := range rows {
		sections = append(sections, fromSectionRow(row))
	}
	return sections, nil
}

// List 列出编排。
func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	cwd := strings.TrimSpace(filter.CWD)
	if filter.RuntimeVisible && cwd == "" {
		return nil, fmt.Errorf("cwd is required for runtime-visible prompt list")
	}
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		Column1:        filter.AgentKey,
		Column2:        filter.Keyword,
		RuntimeVisible: filter.RuntimeVisible,
		Cwd:            cwd,
		LimitCount:     filter.Limit,
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

// WithTx 设置tx。
func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	return wrapPromptError(sqlctx.WithTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_tx", "prompt_template")
}

// Delete 删除编排。
func (s *store) Delete(ctx context.Context, promptKey string) error {
	_, err := s.q.DeletePromptTemplate(ctx, promptKey)
	return wrapPromptError(err, "delete", "prompt_template")
}

// InsertVersion 插入版本。
func (s *store) InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error) {
	id, err := s.q.InsertPromptVersion(ctx, sqlc.InsertPromptVersionParams{
		PromptKey:       version.PromptKey,
		Title:           version.Title,
		AgentKey:        version.AgentKey,
		ToolName:        version.ToolName,
		PromptText:      version.PromptText,
		Column6:         version.Variables,
		Column7:         version.Tags,
		Description:     version.Description,
		Enabled:         version.Enabled,
		CreatedBy:       version.CreatedBy,
		UpdatedBy:       version.UpdatedBy,
		SourceUpdatedAt: sqlc.TimeValuePtr(version.SourceUpdatedAt),
	})
	if err != nil {
		return 0, wrapPromptError(err, "insert_version", "prompt_template_version")
	}
	return id, nil
}

// Upsert 新增或更新记录。
func (s *store) Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	row, err := s.q.UpsertPromptTemplate(ctx, sqlc.UpsertPromptTemplateParams{
		PromptKey:      template.PromptKey,
		Title:          template.Title,
		AgentKey:       template.AgentKey,
		ToolName:       template.ToolName,
		PromptText:     template.PromptText,
		Column6:        template.Variables,
		Column7:        template.Tags,
		Description:    template.Description,
		WhenToUse:      template.WhenToUse,
		Enabled:        template.Enabled,
		ManuallyEdited: template.ManuallyEdited,
		Column12:       template.MatchWhen,
		Priority:       template.Priority,
		CreatedBy:      template.CreatedBy,
		UpdatedBy:      template.UpdatedBy,
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_template")
	}
	mapped := fromUpsertTemplate(row)
	return &mapped, nil
}

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
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:      sqlc.TimeValue(row.UpdatedAt),
		Description:    row.Description,
		WhenToUse:      row.WhenToUse,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       row.Priority,
	}
}

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
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:      sqlc.TimeValue(row.UpdatedAt),
		Description:    row.Description,
		WhenToUse:      row.WhenToUse,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       row.Priority,
	}
}

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
		Enabled:        row.Enabled,
		ManuallyEdited: row.ManuallyEdited,
		CreatedBy:      row.CreatedBy,
		UpdatedBy:      row.UpdatedBy,
		CreatedAt:      sqlc.TimeValue(row.CreatedAt),
		UpdatedAt:      sqlc.TimeValue(row.UpdatedAt),
		Description:    row.Description,
		WhenToUse:      row.WhenToUse,
		MatchWhen:      json.RawMessage(row.MatchWhen),
		Priority:       row.Priority,
	}
}

func fromSectionRow(row sqlc.ListPromptTemplateSectionsByTemplateRow) PromptTemplateSection {
	return PromptTemplateSection{
		ID:          row.ID,
		TemplateID:  row.TemplateID,
		SectionKey:  row.SectionKey,
		Region:      row.Region,
		Ordinal:     row.Ordinal,
		Body:        row.Body,
		TriggerType: row.TriggerType,
		RecallTopic: row.RecallTopic,
		Enabled:     row.Enabled,
	}
}

func wrapPromptError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
