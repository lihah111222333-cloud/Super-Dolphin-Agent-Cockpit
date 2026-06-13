package sqlctx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

const defaultWriteRetryAttempts = 32

type immediateTxDBTX interface {
	sqlc.DBTX
	sqlctxImmediateTx()
}

type immediateConnTx struct {
	conn *sql.Conn
}

func (*immediateConnTx) sqlctxImmediateTx() {}

func (tx *immediateConnTx) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return tx.conn.ExecContext(ctx, query, args...)
}

func (tx *immediateConnTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return tx.conn.PrepareContext(ctx, query)
}

func (tx *immediateConnTx) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return tx.conn.QueryContext(ctx, query, args...)
}

func (tx *immediateConnTx) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return tx.conn.QueryRowContext(ctx, query, args...)
}

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
	if tx, ok := db.(immediateTxDBTX); ok && tx != nil {
		return fn(sqlc.New(tx), tx)
	}
	if database, ok := db.(*sql.DB); ok && database != nil {
		return platformdb.WithTx(ctx, database, func(tx *sql.Tx) error { return fn(q.WithTx(tx), tx) })
	}
	return errors.New("sqlc queries requires transaction-capable DBTX")
}

// WithImmediateTx opens a SQLite write transaction under the shared bounded
// retry loop. Callers use this anywhere PostgreSQL row locks used to serialize
// a read-modify-write path.
func WithImmediateTx(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	database, ok := db.(*sql.DB)
	if !ok || database == nil {
		return errors.New("sqlc queries requires *sql.DB-backed store for immediate transactions")
	}
	return WithWriteRetry(ctx, func() error {
		return withBeginImmediate(ctx, database, func(tx *immediateConnTx) error {
			return fn(sqlc.New(tx), tx)
		})
	})
}

// WithImmediateTxOrReuse keeps an existing transaction when the caller already
// owns one; otherwise it opens a bounded-retry immediate transaction.
func WithImmediateTxOrReuse(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	if tx, ok := db.(*sql.Tx); ok && tx != nil {
		return fn(q.WithTx(tx), tx)
	}
	if tx, ok := db.(immediateTxDBTX); ok && tx != nil {
		return fn(sqlc.New(tx), tx)
	}
	return WithImmediateTx(ctx, db, q, fn)
}

// WithWriteRetry applies the shared SQLite busy/locked retry policy to a single
// write statement or CAS update that is intentionally not wrapped in a wider
// transaction.
func WithWriteRetry(ctx context.Context, fn func() error) error {
	return platformdb.BoundedWriteRetry(ctx, defaultWriteRetryAttempts, fn)
}

func withBeginImmediate(ctx context.Context, db *sql.DB, fn func(tx *immediateConnTx) error) (retErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect immediate tx: %w", err)
	}
	defer closeImmediateConn(conn, &retErr)
	tx := &immediateConnTx{conn: conn}
	if err := beginImmediateTx(ctx, tx); err != nil {
		return err
	}
	txOpen := true
	defer func() {
		if r := recover(); r != nil {
			if txOpen {
				_ = rollbackImmediateTx(context.WithoutCancel(ctx), tx)
			}
			panic(r)
		}
		if txOpen && retErr != nil {
			retErr = rollbackImmediateTxWithError(context.WithoutCancel(ctx), tx, retErr)
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	txOpen = false
	return commitImmediateTx(context.WithoutCancel(ctx), tx)
}

func closeImmediateConn(conn *sql.Conn, retErr *error) {
	if err := conn.Close(); err != nil {
		closeErr := fmt.Errorf("close immediate tx connection: %w", err)
		if *retErr == nil {
			*retErr = closeErr
			return
		}
		*retErr = errors.Join(*retErr, closeErr)
	}
}

func beginImmediateTx(ctx context.Context, tx *immediateConnTx) error {
	if _, err := tx.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate tx: %w", err)
	}
	return nil
}

func commitImmediateTx(ctx context.Context, tx *immediateConnTx) error {
	if _, err := tx.conn.ExecContext(ctx, "COMMIT"); err != nil {
		commitErr := fmt.Errorf("commit immediate tx: %w", err)
		if rollbackErr := rollbackImmediateTx(ctx, tx); rollbackErr != nil {
			return errors.Join(commitErr, rollbackErr)
		}
		return commitErr
	}
	return nil
}

func rollbackImmediateTxWithError(ctx context.Context, tx *immediateConnTx, queryErr error) error {
	if err := rollbackImmediateTx(ctx, tx); err != nil {
		return errors.Join(queryErr, err)
	}
	return queryErr
}

func rollbackImmediateTx(ctx context.Context, tx *immediateConnTx) error {
	if _, err := tx.conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		return fmt.Errorf("rollback immediate tx: %w", err)
	}
	return nil
}
