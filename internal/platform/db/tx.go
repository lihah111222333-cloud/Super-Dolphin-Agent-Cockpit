package db

import (
	"context"
	"errors"
	"fmt"
	"time"

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

func WithTx(ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx) error) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback(ctx)
		return err
	}
	return tx.Commit(ctx)
}

func OpenReadOnlyRows(ctx context.Context, queryer Queryable, query string, args ...any) (pgx.Rows, QueryFinish, error) {
	beginner, ok := queryer.(readOnlyTxBeginner)
	if !ok {
		return openDirectRows(ctx, queryer, query, args...)
	}
	tx, err := beginner.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, nil, err
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
	return context.WithTimeout(context.WithoutCancel(ctx), time.Second)
}
