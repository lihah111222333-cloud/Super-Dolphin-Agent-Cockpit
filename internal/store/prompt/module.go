package prompt

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

var Module = fx.Module("store.prompt",
	fx.Provide(
		newStoreWithPool,
		AsReader,
	),
)

// AsReader 把prompt存储处理为读取器。
func AsReader(store Store) Reader {
	return store
}

// promptTx is the narrow interface satisfied by pgx.Tx that the transaction
// runner needs. Defining it here avoids importing github.com/jackc/pgx/v5
// directly in module.go (archtest rule5 only allows pgxpool in module.go).
type promptTx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

func newStoreWithPool(pool *pgxpool.Pool, q *sqlc.Queries) Store {
	return newStore(q, func(ctx context.Context, fn func(*sqlc.Queries) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		txQueries := q.WithTx(tx)
		if txQueries == nil {
			_ = tx.Rollback(ctx)
			return errors.New("prompt store failed to bind tx queries")
		}
		err = fn(txQueries)
		finishErr := finishPromptTx(ctx, tx, err == nil)
		if err != nil || finishErr != nil {
			return errors.Join(err, finishErr)
		}
		return nil
	})
}

func finishPromptTx(ctx context.Context, tx promptTx, success bool) error {
	if success {
		return tx.Commit(ctx)
	}
	return tx.Rollback(ctx)
}
