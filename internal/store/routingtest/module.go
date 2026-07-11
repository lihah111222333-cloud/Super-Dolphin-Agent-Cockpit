package routingtest

import (
	sqlc "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	"go.uber.org/fx"
)

func newReader(q *sqlc.Queries) Reader {
	if q == nil {
		return nil
	}
	return NewStore(q)
}

var Module = fx.Module("store.routingtest",
	fx.Provide(
		fx.Annotate(
			newReader,
			fx.ParamTags(`optional:"true"`),
		),
	),
)
