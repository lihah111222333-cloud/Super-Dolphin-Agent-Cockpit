package sqlc

import (
	"context"
	"errors"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

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

// WithTxOrReuse runs fn in the current transaction when one is already bound.
func WithTxOrReuse(ctx context.Context, q *Queries, fn func(txq *Queries) error) error {
	if q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	if tx, ok := q.db.(pgx.Tx); ok && tx != nil {
		return fn(q.WithTx(tx))
	}
	if pool, ok := q.db.(*pgxpool.Pool); ok && pool != nil {
		return platformdb.WithTx(ctx, pool, func(tx pgx.Tx) error { return fn(q.WithTx(tx)) })
	}
	return fn(q)
}
