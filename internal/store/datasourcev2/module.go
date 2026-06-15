package datasourcev2

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// Module 把 datasource_v2 store 接入根 store 模块，并提供带事务能力的实现。
var Module = fx.Module("store.datasourcev2",
	fx.Provide(newStoreWithPool),
)

// datasourceV2Tx 是事务提交/回滚所需的最小接口，避免把 pgx.Tx 泄漏到 store 合约。
type datasourceV2Tx interface {
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// newStoreWithPool 创建带事务能力的 datasource_v2 store。
// 正式运行必须通过这个构造函数注入，确保分块重写不会留下半成品。
func newStoreWithPool(pool *pgxpool.Pool, q *sqlc.Queries) Store {
	return newStore(q, q, func(ctx context.Context, fn func(*sqlc.Queries) error) error {
		tx, err := pool.Begin(ctx)
		if err != nil {
			return err
		}
		txQueries := q.WithTx(tx)
		if txQueries == nil {
			_ = tx.Rollback(ctx)
			return errors.New("datasource v2 store failed to bind tx queries")
		}
		err = fn(txQueries)
		finishErr := finishDatasourceV2Tx(ctx, tx, err == nil)
		if err != nil || finishErr != nil {
			return errors.Join(err, finishErr)
		}
		return nil
	})
}

func finishDatasourceV2Tx(ctx context.Context, tx datasourceV2Tx, success bool) error {
	if success {
		return tx.Commit(ctx)
	}
	return tx.Rollback(ctx)
}
