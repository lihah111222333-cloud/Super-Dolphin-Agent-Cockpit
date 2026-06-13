package sqlctx

import (
	"context"
	"database/sql"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// WithTx rebinds the current query set onto a database/sql transaction.
func WithTx(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	database, ok := db.(*sql.DB)
	if !ok || database == nil {
		return errors.New("sqlc queries requires *sql.DB-backed store for transactions")
	}
	return platformdb.WithTx(ctx, database, func(tx *sql.Tx) error {
		return fn(q.WithTx(tx), tx)
	})
}

// WithTxOrReuse runs fn in the current transaction when one is already bound.
func WithTxOrReuse(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	if tx, ok := db.(*sql.Tx); ok && tx != nil {
		return fn(q.WithTx(tx), tx)
	}
	if database, ok := db.(*sql.DB); ok && database != nil {
		return platformdb.WithTx(ctx, database, func(tx *sql.Tx) error { return fn(q.WithTx(tx), tx) })
	}
	return errors.New("sqlc queries requires transaction-capable DBTX")
}
