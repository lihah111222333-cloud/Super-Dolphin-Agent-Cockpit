package workspace

import (
	"database/sql"

	"go.uber.org/fx"
)

var Module = fx.Module("store.workspace",
	fx.Provide(NewStoreFromDB),
)

// NewStoreFromDB 从 SQLite 连接创建 workspace 存储。
func NewStoreFromDB(db *sql.DB) Store {
	return NewStore(db)
}
