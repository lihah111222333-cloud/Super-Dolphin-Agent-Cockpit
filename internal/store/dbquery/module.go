package dbquery

import (
	"database/sql"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

var Module = fx.Module("store.dbquery",
	fx.Provide(newDefaultStore),
)

func newDefaultStore(db *sql.DB, q *sqlc.Queries) Store {
	return NewStore(q, db, defaultQueryTimeout)
}
