package prompt

import (
	"context"
	"encoding/json"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Get(ctx context.Context, promptKey string) (*PromptTemplate, error) {
	row, err := s.q.GetPromptTemplate(ctx, promptKey)
	if err != nil {
		return nil, wrapPromptError(err, "get", "prompt_template")
	}
	mapped := fromGetTemplate(row)
	return &mapped, nil
}

func (s *store) Delete(ctx context.Context, promptKey string) error {
	_, err := s.q.DeletePromptTemplate(ctx, promptKey)
	return wrapPromptError(err, "delete", "prompt_template")
}

// InsertVersion stores an append-only prompt snapshot used for history/audit flows.
func (s *store) InsertVersion(ctx context.Context, version PromptTemplateVersion) error {
	return wrapPromptError(s.q.InsertPromptVersion(ctx, sqlc.InsertPromptVersionParams{
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
		SourceUpdatedAt: version.SourceUpdatedAt,
	}), "insert_version", "prompt_template_version")
}

func (s *store) Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	row, err := s.q.UpsertPromptTemplate(ctx, sqlc.UpsertPromptTemplateParams{
		PromptKey:   template.PromptKey,
		Title:       template.Title,
		AgentKey:    template.AgentKey,
		ToolName:    template.ToolName,
		PromptText:  template.PromptText,
		Column6:     template.Variables,
		Column7:     template.Tags,
		Description: template.Description,
		Enabled:     template.Enabled,
		CreatedBy:   template.CreatedBy,
		UpdatedBy:   template.UpdatedBy,
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_template")
	}
	mapped := fromUpsertTemplate(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{Column1: filter.AgentKey, Column2: filter.Keyword, Limit: filter.Limit})
	if err != nil {
		return nil, wrapPromptError(err, "list", "prompt_template")
	}
	templates := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, fromListTemplate(row))
	}
	return templates, nil
}

func fromGetTemplate(row sqlc.GetPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:          row.ID,
		PromptKey:   row.PromptKey,
		Title:       row.Title,
		AgentKey:    row.AgentKey,
		ToolName:    row.ToolName,
		PromptText:  row.PromptText,
		Variables:   json.RawMessage(row.Variables),
		Tags:        json.RawMessage(row.Tags),
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Description: row.Description,
	}
}

func fromUpsertTemplate(row sqlc.UpsertPromptTemplateRow) PromptTemplate {
	return PromptTemplate{
		ID:          row.ID,
		PromptKey:   row.PromptKey,
		Title:       row.Title,
		AgentKey:    row.AgentKey,
		ToolName:    row.ToolName,
		PromptText:  row.PromptText,
		Variables:   json.RawMessage(row.Variables),
		Tags:        json.RawMessage(row.Tags),
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Description: row.Description,
	}
}

func fromListTemplate(row sqlc.ListPromptTemplatesRow) PromptTemplate {
	return PromptTemplate{
		ID:          row.ID,
		PromptKey:   row.PromptKey,
		Title:       row.Title,
		AgentKey:    row.AgentKey,
		ToolName:    row.ToolName,
		PromptText:  row.PromptText,
		Variables:   json.RawMessage(row.Variables),
		Tags:        json.RawMessage(row.Tags),
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Description: row.Description,
	}
}

func wrapPromptError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
