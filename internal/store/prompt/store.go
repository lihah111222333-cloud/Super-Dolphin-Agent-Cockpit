package prompt

import (
	"context"
	"time"

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
	mapped := fromTemplate(row)
	return &mapped, nil
}

func (s *store) InsertVersion(ctx context.Context, version PromptTemplateVersion) error {
	return wrapPromptError(s.q.InsertPromptVersion(ctx, sqlc.InsertPromptVersionParams{
		PromptKey:       version.PromptKey,
		Title:           version.Title,
		AgentKey:        version.AgentKey,
		ToolName:        version.ToolName,
		PromptText:      version.PromptText,
		Variables:       version.Variables,
		Tags:            version.Tags,
		Enabled:         version.Enabled,
		CreatedBy:       version.CreatedBy,
		UpdatedBy:       version.UpdatedBy,
		SourceUpdatedAt: sourceUpdatedAt(version.SourceUpdatedAt),
	}), "insert_version", "prompt_template_version")
}

func (s *store) Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	row, err := s.q.UpsertPromptTemplate(ctx, sqlc.UpsertPromptTemplateParams{
		PromptKey:   template.PromptKey,
		Title:       template.Title,
		AgentKey:    template.AgentKey,
		ToolName:    template.ToolName,
		PromptText:  template.PromptText,
		Variables:   template.Variables,
		Tags:        template.Tags,
		Description: template.Description,
		Enabled:     template.Enabled,
		CreatedBy:   template.CreatedBy,
		UpdatedBy:   template.UpdatedBy,
	})
	if err != nil {
		return nil, wrapPromptError(err, "upsert", "prompt_template")
	}
	mapped := fromTemplate(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{AgentKey: filter.AgentKey, Keyword: filter.Keyword, Limit: filter.Limit})
	if err != nil {
		return nil, wrapPromptError(err, "list", "prompt_template")
	}
	templates := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, fromTemplate(row))
	}
	return templates, nil
}

func fromTemplate(row sqlc.PromptTemplate) PromptTemplate {
	return PromptTemplate{
		ID:          row.ID,
		PromptKey:   row.PromptKey,
		Title:       row.Title,
		AgentKey:    row.AgentKey,
		ToolName:    row.ToolName,
		PromptText:  row.PromptText,
		Variables:   row.Variables,
		Tags:        row.Tags,
		Enabled:     row.Enabled,
		CreatedBy:   row.CreatedBy,
		UpdatedBy:   row.UpdatedBy,
		CreatedAt:   row.CreatedAt,
		UpdatedAt:   row.UpdatedAt,
		Description: row.Description,
	}
}

func sourceUpdatedAt(ts *time.Time) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return *ts
}

func wrapPromptError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
