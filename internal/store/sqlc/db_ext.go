package sqlc

import (
	"context"
	"errors"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Queryable = DBTX

func (q *Queries) Queryable() Queryable {
	if q == nil {
		return nil
	}
	return q.db
}

// WithTx rebinds the current query set onto a pool-backed transaction.
func WithTx(ctx context.Context, q *Queries, fn func(txq *Queries) error) error {
	if q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	pool, ok := q.db.(*pgxpool.Pool)
	if !ok || pool == nil {
		return errors.New("sqlc queries requires pool-backed store for transactions")
	}
	return platformdb.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return fn(q.WithTx(tx))
	})
}
