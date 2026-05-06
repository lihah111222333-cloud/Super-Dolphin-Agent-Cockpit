package dbquery

import (
	"context"
	"errors"
	"time"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5"
)

const defaultQueryTimeout = 10 * time.Second

type store struct {
	q       sqlc.Querier
	db      platformdb.Queryable
	timeout time.Duration
}

func NewStore(q sqlc.Querier, db platformdb.Queryable, timeout time.Duration) Store {
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	return &store{q: q, db: db, timeout: timeout}
}

func NewQueryStore(db platformdb.Queryable, timeout time.Duration) Store {
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	return &store{db: db, timeout: timeout}
}

func (s *store) Query(ctx context.Context, query string, args ...any) ([]map[string]any, error) {
	if s == nil || s.db == nil {
		return nil, wrapDBQueryError(errors.New("dbquery store is not initialized"), "query")
	}

	var rows []map[string]any
	var err error

	// Defense-in-depth: enforce READ ONLY transaction explicitly at the store level
	if beginner, ok := s.db.(interface {
		BeginTx(context.Context, pgx.TxOptions) (pgx.Tx, error)
	}); ok {
		tx, txErr := beginner.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if txErr != nil {
			return nil, wrapDBQueryError(txErr, "query")
		}
		defer func() {
			_ = tx.Rollback(ctx)
		}()

		// Explicitly execute SET TRANSACTION READ ONLY to ensure database-level enforcement
		if _, execErr := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); execErr != nil {
			return nil, wrapDBQueryError(execErr, "query")
		}

		rows, err = executeQuery(ctx, tx, s.timeout, query, args...)
		if err == nil {
			_ = tx.Commit(ctx)
		}
	} else {
		// Fallback for testing or non-tx queryables
		rows, err = executeQuery(ctx, s.db, s.timeout, query, args...)
	}

	if err != nil {
		return nil, wrapDBQueryError(err, "query")
	}
	return rows, nil
}

// TODO(p7w2): replace PlaceholderDBQuery with a real sql/queries-backed dbquery
// contract once the V3 dbquery migration surface is defined.
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
		out = append(out, PlaceholderRow{Placeholder: row})
	}
	return out, nil
}

func wrapDBQueryError(err error, operation string) error {
	return platformdb.WrapStoreError(err, operation, "db_query")
}
