package prompt

import (
	"database/sql"

	"go.uber.org/fx"
)

var Module = fx.Module("store.prompt",
	fx.Provide(NewStoreFromDB),
)

func NewStoreFromDB(db *sql.DB) Store {
	return NewStore(db)
}
