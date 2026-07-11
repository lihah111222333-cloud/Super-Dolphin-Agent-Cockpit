package dbquery

import (
	"database/sql"

	"go.uber.org/fx"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
)

var Module = fx.Module("store.dbquery",
	fx.Provide(newDefaultStore),
)

func newDefaultStore(db *sql.DB, q *sqlc.Queries) Store {
	return NewStore(q, db, defaultQueryTimeout)
}
