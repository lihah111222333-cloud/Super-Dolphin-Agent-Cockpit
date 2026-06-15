package prompt

import (
	"context"
	"database/sql"
	"errors"

	platformdb "github.com/anthropic-ai/super-agent-v3/internal/platform/db"
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"go.uber.org/fx"
)

var Module = fx.Module("store.prompt",
	fx.Provide(
		newStoreWithDB,
		AsReader,
	),
)

const promptStoreWriteRetryAttempts = 32

func AsReader(store Store) Reader {
	return store
}

func newStoreWithDB(db *sql.DB, q *sqlc.Queries) Store {
	return newStore(q, func(ctx context.Context, fn func(*sqlc.Queries) error) error {
		return platformdb.BoundedWriteRetry(ctx, promptStoreWriteRetryAttempts, func() error {
			return platformdb.WithImmediateTx(ctx, db, func(tx *sql.Tx) error {
				txQueries := q.WithTx(tx)
				if txQueries == nil {
					return errors.New("prompt store failed to bind tx queries")
				}
				return fn(txQueries)
			})
		})
	})
}
