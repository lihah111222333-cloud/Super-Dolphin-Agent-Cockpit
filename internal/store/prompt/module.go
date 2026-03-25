package prompt

import "go.uber.org/fx"

var Module = fx.Module("store.prompt",
	fx.Provide(NewStore),
)
