package thread

import (
	"go.uber.org/fx"
)

var Module = fx.Module("store.thread",
	fx.Provide(NewStore),
	fx.Provide(NewMetadataStore),
	fx.Provide(NewSessionThreadLookup),
)
