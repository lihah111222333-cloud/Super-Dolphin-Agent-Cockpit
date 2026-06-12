package prompt

import (
	"context"
	"database/sql"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"go.uber.org/fx"
)

var Module = fx.Module("store.prompt",
	fx.Provide(
		newStoreWithDB,
		AsReader,
	),
)

func AsReader(store Store) Reader {
	return store
}

func newStoreWithDB(db *sql.DB, q *sqlc.Queries) Store {
	return newStore(q, func(ctx context.Context, fn func(*sqlc.Queries) error) error {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		txQueries := q.WithTx(tx)
		if txQueries == nil {
			_ = tx.Rollback()
			return errors.New("prompt store failed to bind tx queries")
		}
		if err = fn(txQueries); err != nil {
			_ = tx.Rollback()
			return err
		}
		return tx.Commit()
	})
}
