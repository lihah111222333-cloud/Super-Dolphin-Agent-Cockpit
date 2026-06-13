package workspace

import (
	"database/sql"

	"go.uber.org/fx"
)

var Module = fx.Module("store.workspace",
	fx.Provide(NewStoreFromDB),
)

func NewStoreFromDB(db *sql.DB) Store {
	return NewStore(db)
}
