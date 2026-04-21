package feedback

import "go.uber.org/fx"

var Module = fx.Module("store.feedback",
	fx.Provide(NewStore),
)
