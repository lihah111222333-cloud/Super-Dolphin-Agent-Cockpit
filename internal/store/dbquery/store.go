package dbquery

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type store struct {
	q *sqlc.Queries
}

func NewStore(q *sqlc.Queries) Store { return &store{q: q} }

func (s *store) Placeholder(ctx context.Context) ([]PlaceholderRow, error) {
	rows, err := s.q.PlaceholderDBQuery(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]PlaceholderRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, PlaceholderRow{Placeholder: row.Placeholder})
	}
	return out, nil
}
