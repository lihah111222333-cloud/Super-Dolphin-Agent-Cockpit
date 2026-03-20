package sqlc

import (
	"context"
	"errors"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

type rowScanner interface {
	Scan(dest ...any) error
}

type Queries struct {
	pool *pgxpool.Pool
	tx   pgx.Tx
}

func New(pool *pgxpool.Pool) *Queries {
	return &Queries{pool: pool}
}

func NewWithTx(tx pgx.Tx) *Queries {
	return &Queries{tx: tx}
}

var _ Querier = (*Queries)(nil)

type queryable interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func scanValue[T any](row rowScanner) (T, error) {
	var item T
	err := row.Scan(&item)
	return item, err
}

func (q *Queries) queryable() queryable {
	if q.tx != nil {
		return q.tx
	}
	return q.pool
}

func (q *Queries) WithTx(ctx context.Context, fn func(txq *Queries) error) error {
	if q.pool == nil {
		return errors.New("sqlc queries requires pool-backed store for transactions")
	}
	return platformdb.WithTx(ctx, q.pool, func(tx pgx.Tx) error {
		return fn(NewWithTx(tx))
	})
}

func (q *Queries) exec(ctx context.Context, sql string, args ...any) error {
	_, err := q.queryable().Exec(ctx, sql, args...)
	return err
}

func (q *Queries) execRows(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := q.queryable().Exec(ctx, sql, args...)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}

func queryOne[T any](ctx context.Context, q *Queries, sql string, scan func(rowScanner) (T, error), args ...any) (T, error) {
	return scan(q.queryable().QueryRow(ctx, sql, args...))
}

func queryMany[T any](ctx context.Context, q *Queries, sql string, scan func(rowScanner) (T, error), args ...any) ([]T, error) {
	rows, err := q.queryable().Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []T
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
