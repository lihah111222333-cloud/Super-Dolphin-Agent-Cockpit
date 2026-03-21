package dbquery

import (
	"context"
	"errors"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if s == nil || s.q == nil {
		return nil, platformdb.WrapStoreError(errors.New("dbquery store is not initialized"), "query", "db_query")
	}
	rows, err := executeQuery(ctx, s.q.Queryable(), query, args...)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "query", "db_query")
	}
	return rows, nil
}

// TODO(p7w2): replace PlaceholderDBQuery with a real sql/queries-backed dbquery
// contract once the V3 dbquery migration surface is defined.
func (s *store) Placeholder(ctx context.Context) ([]PlaceholderRow, error) {
	rows, err := s.q.PlaceholderDBQuery(ctx)
	if err != nil {
		return nil, platformdb.WrapStoreError(err, "placeholder", "db_query")
	}
	out := make([]PlaceholderRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlaceholderRow{Placeholder: row.Placeholder})
	}
	return out, nil
}
