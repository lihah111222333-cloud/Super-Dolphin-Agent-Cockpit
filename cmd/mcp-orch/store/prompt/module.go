package prompt

import (
	"database/sql"

	"go.uber.org/fx"
)

// Module 注册 prompt store 的 fx provider。
var Module = fx.Module("store.prompt",
	fx.Provide(NewStoreFromDB),
)

// NewStoreFromDB 从 SQLite 连接创建 prompt 存储。
func NewStoreFromDB(db *sql.DB) Store {
	return NewStore(db)
}
