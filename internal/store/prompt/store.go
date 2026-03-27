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

func NewStore(q *sqlc.Queries) Reader { return &store{q: q} }

func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		Column1: filter.AgentKey,
		Column2: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "list", "prompt_template")
	}
	templates := make([]PromptTemplate, 0, len(rows))
	for _, row := range rows {
		templates = append(templates, fromSQLCRow(row))
	}
	return templates, nil
}

func fromSQLCRow(row sqlc.ListPromptTemplatesRow) PromptTemplate {
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
