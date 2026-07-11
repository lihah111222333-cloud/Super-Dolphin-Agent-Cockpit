package datasourcev2

import (
	"context"
	"database/sql"
	"errors"

	platformdb "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	"go.uber.org/fx"
)

// Module 把 datasource_v2 store 接入根 store 模块，并提供 SQLite 事务能力。
var Module = fx.Module("store.datasourcev2",
	fx.Provide(newStoreWithDB),
)

// newStoreWithDB 创建带事务能力的 datasource_v2 store。
func newStoreWithDB(db *sql.DB, q *sqlc.Queries) Store {
	return newStore(q, q, func(ctx context.Context, fn func(*sqlc.Queries) error) error {
		return platformdb.WithTx(ctx, db, func(tx *sql.Tx) error {
			txQueries := q.WithTx(tx)
			if txQueries == nil {
				return errors.New("datasource v2 store failed to bind tx queries")
			}
			return fn(txQueries)
		})
	})
}
