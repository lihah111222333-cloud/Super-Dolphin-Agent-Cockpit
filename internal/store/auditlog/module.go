package auditlog

import "go.uber.org/fx"

var Module = fx.Module("store.auditlog",
	fx.Provide(NewStore),
)
