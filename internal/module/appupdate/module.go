package appupdate

import "go.uber.org/fx"

type serviceParams struct {
	fx.In

	Config      Config
	RequestQuit RequestQuit `optional:"true"`
}

var Module = fx.Module("appupdate",
	fx.Provide(
		ProvideConfig,
		NewService,
		NewHandlers,
	),
)
