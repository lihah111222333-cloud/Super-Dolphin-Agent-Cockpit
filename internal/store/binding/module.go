package binding

import (
	"go.uber.org/fx"
)

var Module = fx.Module("store.binding",
	fx.Provide(NewStore),
	fx.Provide(NewSessionBindingLookup),
)
