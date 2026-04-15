package dbquery

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"github.com/jackc/pgx/v5/pgxpool"
)

var Module = fx.Module("store.dbquery",
	fx.Provide(newDefaultStore),
)

func newDefaultStore(pool *pgxpool.Pool, q *sqlc.Queries) Store {
	return NewStore(q, pool, defaultQueryTimeout)
}
