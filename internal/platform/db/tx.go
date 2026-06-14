package db

import (
	"context"
	"errors"
	"fmt"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Queryable interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
	QueryRow(context.Context, string, ...any) pgx.Row
}

type QueryFinish func(success bool) error

type readOnlyTxBeginner interface {
	BeginTx(ctx context.Context, txOptions pgx.TxOptions) (pgx.Tx, error)
}

// WithTx 设置tx。
func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	return runWithTx(ctx, tx, fn)
}

// runWithTx 是 WithTx 的内部实现：拿到 tx 后跑 fn 并保证 panic-safe rollback。
// 抽出来便于 unit test —— 测试用 fake pgx.Tx 直接覆盖 commit / error / panic 三条路径，
// 不必 mock pgxpool.Pool（pgxpool.Pool 是具体类型不是 interface 没法直接 mock）。
func runWithTx(ctx context.Context, tx pgx.Tx, fn func(tx pgx.Tx) error) (retErr error) {
	// panic-safe rollback：若 fn 内部 panic，进入 recover 分支同步 rollback 后重抛
	// panic，保证调用方仍能看到原始 stack。正常 commit / 错误 rollback 路径走完后
	// tx 已 closed，再调 Rollback 返回 pgx.ErrTxClosed —— defer 里裸吃不传播。
	defer func() {
		if r := recover(); r != nil {
			cleanupCtx, cancel := txCleanupContext(ctx)
			defer cancel()
			_ = tx.Rollback(cleanupCtx)
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

// OpenReadOnlyRows 打开readonlyrows。
func OpenReadOnlyRows(ctx context.Context, queryer Queryable, query string, args ...any) (pgx.Rows, QueryFinish, error) {
	beginner, ok := queryer.(readOnlyTxBeginner)
	if !ok {
		return openDirectRows(ctx, queryer, query, args...)
	}
	tx, err := beginner.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
	}
	if _, err := tx.Exec(ctx, "SET TRANSACTION READ ONLY"); err != nil {
		return nil, nil, rollbackTx(ctx, tx, err)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, rollbackTx(ctx, tx, err)
	}

	return rows, func(success bool) error {
		rows.Close()
		if success {
			cleanupCtx, cancel := txCleanupContext(ctx)
			defer cancel()
			return tx.Commit(cleanupCtx)
		}
		return rollbackTx(ctx, tx, nil)
	}, nil
}

// RowsFieldNames 处理rows字段名称。
func RowsFieldNames(rows pgx.Rows) []string {
	if rows == nil {
		return nil
	}
	fields := rows.FieldDescriptions()
	names := make([]string, 0, len(fields))
	for _, field := range fields {
		names = append(names, string(field.Name))
	}
	return names
}

func openDirectRows(ctx context.Context, queryer Queryable, query string, args ...any) (pgx.Rows, QueryFinish, error) {
	rows, err := queryer.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, err
	}
	return rows, func(bool) error {
		rows.Close()
		return nil
	}, nil
}

func rollbackTx(ctx context.Context, tx pgx.Tx, queryErr error) error {
	cleanupCtx, cancel := txCleanupContext(ctx)
	defer cancel()
	if err := tx.Rollback(cleanupCtx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		return errors.Join(queryErr, err)
	}
	return queryErr
}

func txCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return platformconfig.WithTxCleanupTimeout(context.WithoutCancel(ctx))
}
