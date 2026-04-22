package prompt

import (
	"context"
	"encoding/json"
	"errors"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type querier interface {
	ListPromptTemplates(ctx context.Context, arg sqlc.ListPromptTemplatesParams) ([]sqlc.ListPromptTemplatesRow, error)
}

type getQuerier interface {
	GetPromptTemplate(ctx context.Context, promptKey string) (sqlc.GetPromptTemplateRow, error)
}

type deleteQuerier interface {
	DeletePromptTemplate(ctx context.Context, promptKey string) (int64, error)
}

type insertVersionQuerier interface {
	InsertPromptVersion(ctx context.Context, arg sqlc.InsertPromptVersionParams) (int64, error)
}

type upsertQuerier interface {
	UpsertPromptTemplate(ctx context.Context, arg sqlc.UpsertPromptTemplateParams) (sqlc.UpsertPromptTemplateRow, error)
}

type txRunner func(context.Context, func(*sqlc.Queries) error) error

type store struct {
	q       querier
	queries *sqlc.Queries
	runInTx txRunner
}

func NewStore(q *sqlc.Queries) Store {
	return newStore(q, nil)
}

func newStore(q *sqlc.Queries, runInTx txRunner) Store {
	return &store{q: q, queries: q, runInTx: runInTx}
}

func (s *store) Get(ctx context.Context, promptKey string) (*PromptTemplate, error) {
	q, ok := s.q.(getQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support get"), "get", "prompt_template")
	}
	row, err := q.GetPromptTemplate(ctx, promptKey)
	if err != nil {
		return nil, wrapPromptError(err, "get", "prompt_template")
	}
	mapped := fromGetTemplate(row)
	return &mapped, nil
}

func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		Column1: filter.AgentKey,
		Column2: filter.Keyword,
		Limit:   filter.Limit,
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

func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error {
	if s.runInTx == nil || s.queries == nil {
		return wrapPromptError(fn(s), "with_tx", "prompt_template")
	}
	err := s.runInTx(ctx, func(txQueries *sqlc.Queries) error {
		return fn(&store{q: txQueries, queries: txQueries, runInTx: s.runInTx})
	})
	return wrapPromptError(err, "with_tx", "prompt_template")
}

func (s *store) Delete(ctx context.Context, promptKey string) error {
	q, ok := s.q.(deleteQuerier)
	if !ok {
		return wrapPromptError(errors.New("prompt store does not support delete"), "delete", "prompt_template")
	}
	rows, err := q.DeletePromptTemplate(ctx, promptKey)
	if err != nil {
		return wrapPromptError(err, "delete", "prompt_template")
	}
	if rows == 0 {
		return wrapPromptError(platformdb.ErrNotFound, "delete", "prompt_template")
	}
	return nil
}

func (s *store) InsertVersion(ctx context.Context, version PromptTemplateVersion) (int64, error) {
	q, ok := s.q.(insertVersionQuerier)
	if !ok {
		return 0, wrapPromptError(errors.New("prompt store does not support insert_version"), "insert_version", "prompt_template_version")
	}
	id, err := q.InsertPromptVersion(ctx, sqlc.InsertPromptVersionParams{
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
	})
	if err != nil {
		return 0, wrapPromptError(err, "insert_version", "prompt_template_version")
	}
	return id, nil
}

func (s *store) Upsert(ctx context.Context, template PromptTemplate) (*PromptTemplate, error) {
	q, ok := s.q.(upsertQuerier)
	if !ok {
		return nil, wrapPromptError(errors.New("prompt store does not support upsert"), "upsert", "prompt_template")
	}
	row, err := q.UpsertPromptTemplate(ctx, sqlc.UpsertPromptTemplateParams{
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

func wrapPromptError(err error, operation, entity string) error {
	return platformdb.WrapStoreError(err, operation, entity)
}
