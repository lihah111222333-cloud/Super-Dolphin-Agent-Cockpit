package agentstatus

import "go.uber.org/fx"

var Module = fx.Module("store.agentstatus",
	fx.Provide(NewStore),
)
