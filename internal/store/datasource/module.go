package datasource

import (
	"database/sql"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

// Module 把数据源文档存储接入 Fx 图，供 datasource 模块按接口注入。
var Module = fx.Module("store.datasource",
	fx.Provide(newDocumentStore),
)

// newDocumentStore 从数据库池创建数据源文档存储，避免业务模块直接接触数据库类型。
func newDocumentStore(db *sql.DB) contract.DatasourceDocumentStore {
	return NewDocumentStore(db)
}
