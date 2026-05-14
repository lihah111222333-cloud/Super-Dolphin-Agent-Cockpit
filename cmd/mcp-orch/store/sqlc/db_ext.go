package sqlc

import (
	"context"
	"errors"
	"fmt"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
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
	if beginner, ok := q.db.(txBeginner); ok && beginner != nil {
		return withTxBeginner(ctx, beginner, func(tx pgx.Tx) error { return fn(q.WithTx(tx)) })
	}
	return fn(q)
}

func withTxBeginner(ctx context.Context, beginner txBeginner, fn func(tx pgx.Tx) error) error {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(ctx)
			panic(r)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
