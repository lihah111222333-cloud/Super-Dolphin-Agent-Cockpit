// Package sqlctx 封装 sqlc 与 database/sql 的事务绑定逻辑，提供 SQLite IMMEDIATE
// 写事务、普通事务复用和有界 busy-retry 策略，供 taskdag / workspace 等 store 包使用。
package sqlctx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"runtime/debug"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
)

// defaultWriteRetryAttempts 是 SQLite busy/locked 写重试的默认上限次数。
const defaultWriteRetryAttempts = 32

// immediateConnTx 包装单个 *sql.Conn，实现 sqlc.DBTX 接口，
// 用于在 BEGIN IMMEDIATE 手动事务中执行 sqlc 生成的查询。
type immediateConnTx struct {
	conn *sql.Conn
}

// ExecContext 在当前连接上执行 SQL 语句。
func (tx *immediateConnTx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	return tx.conn.ExecContext(ctx, query, args...)
}

// PrepareContext 在当前连接上预编译 SQL 语句。
func (tx *immediateConnTx) PrepareContext(ctx context.Context, query string) (*sql.Stmt, error) {
	return tx.conn.PrepareContext(ctx, query)
}

// QueryContext 在当前连接上执行查询并返回多行结果。
func (tx *immediateConnTx) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return tx.conn.QueryContext(ctx, query, args...)
}

// QueryRowContext 在当前连接上执行查询并返回单行结果。
func (tx *immediateConnTx) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return tx.conn.QueryRowContext(ctx, query, args...)
}

// WithTx 将当前 sqlc 查询集绑定到 database/sql 事务。
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

// WithTxOrReuse 复用已绑定事务；没有事务时为 SQLite 连接开启普通事务。
func WithTxOrReuse(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	if tx, ok := db.(*sql.Tx); ok && tx != nil {
		return fn(q.WithTx(tx), tx)
	}
	if tx, ok := db.(*immediateConnTx); ok && tx != nil {
		return fn(sqlc.New(tx), tx)
	}
	if database, ok := db.(*sql.DB); ok && database != nil {
		return platformdb.WithTx(ctx, database, func(tx *sql.Tx) error { return fn(q.WithTx(tx), tx) })
	}
	return errors.New("sqlc queries requires transaction-capable DBTX")
}

// WithImmediateTx 在共享有界重试策略下开启 SQLite BEGIN IMMEDIATE 写事务。
// 读改写路径需要串行化时，用它替代旧 PostgreSQL 行锁。
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

// WithImmediateTxOrReuse 复用调用方已有事务；否则开启带有界重试的 IMMEDIATE 事务。
func WithImmediateTxOrReuse(ctx context.Context, db sqlc.DBTX, q *sqlc.Queries, fn func(txq *sqlc.Queries, txdb sqlc.DBTX) error) error {
	if db == nil || q == nil {
		return errors.New("sqlc queries are not initialized")
	}
	if tx, ok := db.(*sql.Tx); ok && tx != nil {
		return fn(q.WithTx(tx), tx)
	}
	if tx, ok := db.(*immediateConnTx); ok && tx != nil {
		return fn(sqlc.New(tx), tx)
	}
	return WithImmediateTx(ctx, db, q, fn)
}

// WithWriteRetry 为单条写入或 CAS 更新应用共享 SQLite busy/locked 有界重试策略。
func WithWriteRetry(ctx context.Context, fn func() error) error {
	return platformdb.BoundedWriteRetry(ctx, defaultWriteRetryAttempts, fn)
}

// withBeginImmediate 在独立连接上手动执行 BEGIN IMMEDIATE / COMMIT / ROLLBACK，
// 确保写事务获得排他写锁；连接关闭由 defer closeImmediateConn 负责，错误合并到 retErr。
func withBeginImmediate(ctx context.Context, db *sql.DB, fn func(tx *immediateConnTx) error) (retErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect immediate tx: %w", err)
	}
	defer closeImmediateConn(conn, &retErr) // 连接关闭晚于 commit/rollback
	tx := &immediateConnTx{conn: conn}
	if err := beginImmediateTx(ctx, tx); err != nil {
		return err
	}
	txOpen := true
	defer func() {
		if r := recover(); r != nil {
			panicErr := fmt.Errorf("immediate tx callback panic: %v\n%s", r, string(debug.Stack()))
			if txOpen {
				panicErr = rollbackImmediateTxWithError(context.WithoutCancel(ctx), tx, panicErr)
				txOpen = false
			}
			retErr = panicErr
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

// closeImmediateConn 关闭 immediateConnTx 使用的底层连接，并把关闭错误合并到 retErr。
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

// beginImmediateTx 在连接上执行 BEGIN IMMEDIATE 语句。
func beginImmediateTx(ctx context.Context, tx *immediateConnTx) error {
	if _, err := tx.conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin immediate tx: %w", err)
	}
	return nil
}

// commitImmediateTx 提交手动事务；提交失败时立即回滚并合并错误。
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

// rollbackImmediateTxWithError 回滚手动事务并将回滚错误与原始查询错误合并返回。
func rollbackImmediateTxWithError(ctx context.Context, tx *immediateConnTx, queryErr error) error {
	if err := rollbackImmediateTx(ctx, tx); err != nil {
		return errors.Join(queryErr, err)
	}
	return queryErr
}

// rollbackImmediateTx 在手动事务中执行 ROLLBACK 语句。
func rollbackImmediateTx(ctx context.Context, tx *immediateConnTx) error {
	if _, err := tx.conn.ExecContext(ctx, "ROLLBACK"); err != nil {
		return fmt.Errorf("rollback immediate tx: %w", err)
	}
	return nil
}
