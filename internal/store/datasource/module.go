package datasource

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// Module 把数据源文档存储接入 Fx 图，供 datasource 模块按接口注入。
var Module = fx.Module("store.datasource",
	fx.Provide(newDocumentStore),
)

// newDocumentStore 从数据库池创建数据源文档存储，避免业务模块直接接触数据库类型。
func newDocumentStore(pool *pgxpool.Pool) contract.DatasourceDocumentStore {
	return NewDocumentStore(pool)
}
