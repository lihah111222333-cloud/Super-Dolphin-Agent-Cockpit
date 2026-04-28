package cron

import (
	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

// Module wires Store into the core Fx tree. The scheduler / runner actors
// that consume this store live under internal/module/cron in phase 2.
var Module = fx.Module("store.cron",
	fx.Provide(NewStore),
)

// NewStoreFromPool returns the production Store backed by a freshly
// constructed *sqlc.Queries. Used by binaries that don't compose the
// full Fx graph (for example mcp-orch) but still want the same
// validated CRUD surface as the core app.
func NewStoreFromPool(pool *pgxpool.Pool) Store {
	return NewStore(sqlc.New(pool))
}
