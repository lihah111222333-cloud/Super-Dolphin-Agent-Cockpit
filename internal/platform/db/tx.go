package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
)

// Queryable はメインの DB/Tx を抽象する最小インタフェース。
// database/sql の *sql.DB と *sql.Tx が両方満たす。
type Queryable interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// QueryFinish はトランザクション or Rows のクリーンアップ関数。
type QueryFinish func(success bool) error

// WithTx は通常の読み書きトランザクション。panic-safe rollback 付き。
func WithTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	return runWithTx(ctx, tx, fn)
}

// WithImmediateTx は BEGIN IMMEDIATE トランザクション（SQLite 書き込み競合防止用）。
func WithImmediateTx(ctx context.Context, db *sql.DB, fn func(tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin immediate tx: %w", err)
	}
	return runWithTx(ctx, tx, fn)
}

// sqlTxCommitter abstracts *sql.Tx for unit testing.
type sqlTxCommitter interface {
	Commit() error
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

// OpenReadOnlyRows はクエリ専用の rows を開く。finish を必ず呼ぶこと。
func OpenReadOnlyRows(ctx context.Context, queryer Queryable, query string, args ...any) (*sql.Rows, QueryFinish, error) {
	rows, err := queryer.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	return rows, func(bool) error {
		return rows.Close()
	}, nil
}

// RowsFieldNames は *sql.Rows のカラム名リストを返す。
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

func rollbackTx(_ context.Context, tx *sql.Tx, queryErr error) error {
	if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
		return errors.Join(queryErr, err)
	}
	return queryErr
}

func txCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return platformconfig.WithTxCleanupTimeout(context.WithoutCancel(ctx))
}
