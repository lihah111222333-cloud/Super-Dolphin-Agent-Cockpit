package sharedfile

import "go.uber.org/fx"

var Module = fx.Module("store.sharedfile",
	fx.Provide(NewStore),
	fx.Provide(func(s Store) Reader { return s }),
	fx.Provide(func(s Store) Deleter { return s }),
)
