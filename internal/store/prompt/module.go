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

// AsReader 将完整 prompt Store 收窄为只读 Reader 注入下游模块。
// 这里复用同一个实例，避免 fx 图中出现两套写入重试和事务边界。
func AsReader(store Store) Reader {
	return store
}

// NewStoreWithDB 创建带 IMMEDIATE 事务和写重试能力的 Store，供测试和 fx 之外的装配入口使用。
func NewStoreWithDB(db *sql.DB, q *sqlc.Queries) Store {
	return newStoreWithDB(db, q)
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
