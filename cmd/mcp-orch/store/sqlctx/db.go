package sqlctx

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type txBeginner interface {
	Begin(context.Context) (pgx.Tx, error)
}

// WithTx rebinds the current query set onto a pool-backed transaction.
func WithTx(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	pool, ok := db.(*pgxpool.Pool)
	if !ok || pool == nil {
		return errors.New("sqlc queries requires pool-backed store for transactions")
	}
	return platformdb.WithTx(ctx, pool, func(tx pgx.Tx) error {
		return fn(q.WithTx(tx), tx)
	})
}

// WithTxOrReuse runs fn in the current transaction when one is already bound.
func WithTxOrReuse(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	if tx, ok := db.(pgx.Tx); ok && tx != nil {
		return fn(q.WithTx(tx), tx)
	}
	if pool, ok := db.(*pgxpool.Pool); ok && pool != nil {
		return platformdb.WithTx(ctx, pool, func(tx pgx.Tx) error { return fn(q.WithTx(tx), tx) })
	}
	if beginner, ok := db.(txBeginner); ok && beginner != nil {
		return withTxBeginner(ctx, beginner, func(tx pgx.Tx) error { return fn(q.WithTx(tx), tx) })
	}
	return errors.New("sqlc queries requires transaction-capable DBTX")
}

func withTxBeginner(ctx context.Context, beginner txBeginner, fn func(tx pgx.Tx) error) error {
	tx, err := beginner.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback(ctx)
			// archguard:ignore panic_count -- rethrow after rollback preserves caller panic semantics.
			panic(r)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}
