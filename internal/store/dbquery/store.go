package dbquery

import (
	"context"
	"errors"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

const defaultQueryTimeout = 10 * time.Second

type store struct {
	q       sqlc.Querier
	db      platformdb.Queryable
	timeout time.Duration
}

// NewStore 创建存储。
func NewStore(q sqlc.Querier, db platformdb.Queryable, timeout time.Duration) Store {
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	return &store{q: q, db: db, timeout: timeout}
}

// NewQueryStore 创建查询存储。
func NewQueryStore(db platformdb.Queryable, timeout time.Duration) Store {
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	return &store{db: db, timeout: timeout}
}

// Query 处理查询。
func (s *store) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if s == nil || s.db == nil {
		return nil, wrapDBQueryError(errors.New("dbquery store is not initialized"), "query")
	}

	rows, err := executeQuery(ctx, s.db, s.timeout, query, args...)
	if err != nil {
		return nil, wrapDBQueryError(err, "query")
	}
	return rows, nil
}

// Placeholder preserves the legacy PlaceholderDBQuery compatibility path until
// callers migrate to the generic Query contract.
// Placeholder 处理placeholder。
func (s *store) Placeholder(ctx context.Context) ([]PlaceholderRow, error) {
	if s == nil || s.q == nil {
		return nil, wrapDBQueryError(errors.New("dbquery store is not initialized"), "placeholder")
	}
	rows, err := s.q.PlaceholderDBQuery(ctx)
	if err != nil {
		return nil, wrapDBQueryError(err, "placeholder")
	}
	out := make([]PlaceholderRow, 0, len(rows))
	for _, row := range rows {
		s, _ := row.(*string)
		out = append(out, PlaceholderRow{Placeholder: s})
	}
	return out, nil
}

func wrapDBQueryError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "db_query")
}
