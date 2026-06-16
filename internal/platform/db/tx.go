package db

import (
	"context"
	"database/sql"
	"fmt"
)

// Queryable 抽象 *sql.DB 和 *sql.Tx 都满足的最小查询接口。
type Queryable interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// QueryFinish 表示查询 rows 的清理函数。
type QueryFinish func(success bool) error

// WithTx 开启普通 database/sql 事务，并在异常时回滚并返回错误。
func WithTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	return runWithTx(tx, fn)
}

// WithImmediateTx 开启 SQLite 写入竞争防护用的 IMMEDIATE 事务。
func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin immediate tx: %w", err)
	}
	return runWithTx(tx, fn)
}

// sqlTxCommitter 抽象 *sql.Tx 以便单元测试覆盖提交和回滚路径。
type sqlTxCommitter interface {
	Commit() error
	Rollback() error
}

func runWithTx(tx *sql.Tx, fn func(tx *sql.Tx) error) (retErr error) {
	return runWithCommitter(tx, func(c sqlTxCommitter) error { return fn(c.(*sql.Tx)) })
}

func runWithCommitter(tx sqlTxCommitter, fn func(c sqlTxCommitter) error) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			_ = tx.Rollback()
			retErr = fmt.Errorf("transaction callback failed: %v", r)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

// OpenReadOnlyRows 打开只读查询 rows；调用方必须执行返回的 finish。
func OpenReadOnlyRows(ctx context.Context, queryer Queryable, query string, args ...any) (*sql.Rows, QueryFinish, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	return rows, func(bool) error {
		return rows.Close()
	}, nil
}

// RowsFieldNames 返回 *sql.Rows 的列名列表。
func RowsFieldNames(rows *sql.Rows) []string {
	if rows == nil {
		return nil
	}
	cols, err := rows.Columns()
	if err != nil {
		return []string{}
	}
	return cols
}
