package dbquery

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

var Module = fx.Module("store.dbquery",
	fx.Provide(newDefaultStore),
)

func newDefaultStore(q *sqlc.Queries) Store {
	return NewStore(q, defaultQueryTimeout)
}
