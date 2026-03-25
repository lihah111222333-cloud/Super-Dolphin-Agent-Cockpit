package prompt

import (
	"context"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) List(ctx context.Context, filter ListFilter) ([]PromptTemplate, error) {
	rows, err := s.q.ListPromptTemplates(ctx, sqlc.ListPromptTemplatesParams{
		Column1: filter.AgentKey,
		Column2: filter.Keyword,
		Limit:   filter.Limit,
	})
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "list", "prompt_template")
	}
	return rows, nil
}
