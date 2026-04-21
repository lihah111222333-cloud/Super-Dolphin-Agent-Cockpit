package router

import (
	rtstore "github.com/anthropic-ai/super-agent-v3/internal/store/routingtest"
	sqlc "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	"go.uber.org/fx"
)

func newRoutingTestReader(q *sqlc.Queries) rtstore.Reader {
	if q == nil {
		return nil
	}
	return rtstore.NewStore(q)
}

var Module = fx.Module("router.preview",
	fx.Provide(
		fx.Annotate(
			newRoutingTestReader,
			fx.ParamTags(`optional:"true"`),
		),
		fx.Annotate(
			NewService,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`),
		),
		NewHandlers,
	),
)
