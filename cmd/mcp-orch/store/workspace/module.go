package workspace

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

var Module = fx.Module("store.workspace",
	fx.Provide(NewStoreFromPool),
)

func NewStoreFromPool(pool *pgxpool.Pool) Store {
	return NewStore(pool)
}
