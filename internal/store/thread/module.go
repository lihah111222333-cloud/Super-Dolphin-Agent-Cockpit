package thread

import (
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

var Module = fx.Module("store.thread",
	fx.Provide(NewStore),
	fx.Provide(NewMetadataStore),
	fx.Provide(NewSessionThreadLookup),
)

// NewStoreFromPool 从pool创建存储。
func NewStoreFromPool(pool *pgxpool.Pool) Store {
	return NewStore(sqlc.New(pool))
}
