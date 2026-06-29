package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"unsafe"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// Queryable 抽象 *sql.DB 和 *sql.Tx 都满足的最小查询接口。
type Queryable interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// QueryFinish 表示查询 rows 的清理函数。
type QueryFinish func(success bool) error

// WithTx 开启普通 database/sql 事务，并在 panic 时回滚。
func WithTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	return runWithTx(ctx, tx, fn)
}

// WithImmediateTx 开启 SQLite 写入竞争防护用的 IMMEDIATE 事务。
func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) (retErr error) {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("connect immediate tx: %w", err)
	}
	defer func() {
		if err := conn.Close(); err != nil {
			retErr = errors.Join(retErr, fmt.Errorf("close immediate tx connection: %w", err))
		}
	}()

	restoreBeginMode, err := setSQLiteConnBeginMode(ctx, conn, "immediate")
	if err != nil {
		return err
	}
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	restoreErr := restoreBeginMode()
	if err != nil {
		return errors.Join(fmt.Errorf("begin immediate tx: %w", err), restoreErr)
	}
	if restoreErr != nil {
		_ = tx.Rollback()
		return restoreErr
	}
	retErr = runWithTx(ctx, tx, fn)
	return retErr
}

// setSQLiteConnBeginMode 临时设置 modernc sqlite 连接的 beginMode。
// database/sql 没有暴露 BEGIN IMMEDIATE 选项；如果驱动字段消失，这里必须报错而不能退回 deferred。
func setSQLiteConnBeginMode(ctx context.Context, conn *sql.Conn, mode string) (func() error, error) {
	var previous string
	if err := conn.Raw(func(driverConn any) error {
		field, err := sqliteBeginModeField(driverConn)
		if err != nil {
			return err
		}
		previous = field.String()
		setUnexportedString(field, mode)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("configure immediate tx begin mode: %w", err)
	}
	return func() error {
		return conn.Raw(func(driverConn any) error {
			field, err := sqliteBeginModeField(driverConn)
			if err != nil {
				return err
			}
			setUnexportedString(field, previous)
			return nil
		})
	}, nil
}

func sqliteBeginModeField(driverConn any) (reflect.Value, error) {
	value := reflect.ValueOf(driverConn)
	if value.Kind() != reflect.Pointer || value.IsNil() {
		return reflect.Value{}, fmt.Errorf("SQLite driver connection has unexpected type %T", driverConn)
	}
	field := value.Elem().FieldByName("beginMode")
	if !field.IsValid() || field.Kind() != reflect.String || !field.CanAddr() {
		return reflect.Value{}, fmt.Errorf("SQLite driver connection %T does not expose beginMode", driverConn)
	}
	return field, nil
}

func setUnexportedString(field reflect.Value, value string) {
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().SetString(value)
}

// sqlTxCommitter 抽象 *sql.Tx 以便单元测试覆盖提交和回滚路径。
type sqlTxCommitter interface {
	Commit() error
	Rollback() error
}

type sqlRollbacker interface {
	Rollback() error
}

func runWithTx(ctx context.Context, tx *sql.Tx, fn func(tx *sql.Tx) error) (retErr error) {
	return runWithCommitter(ctx, tx, func(c sqlTxCommitter) error { return fn(c.(*sql.Tx)) })
}

func runWithCommitter(ctx context.Context, tx sqlTxCommitter, fn func(c sqlTxCommitter) error) (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			cleanupCtx, cancel := txCleanupContext(ctx)
			defer cancel()
			_ = tx.Rollback()
			_ = cleanupCtx
			panic(r)
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
		return nil
	}
	return cols
}

func rollbackTx(_ context.Context, tx sqlRollbacker, queryErr error) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(queryErr, err)
	}
	return queryErr
}

func txCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return platformconfig.WithTxCleanupTimeout(context.WithoutCancel(ctx))
}
