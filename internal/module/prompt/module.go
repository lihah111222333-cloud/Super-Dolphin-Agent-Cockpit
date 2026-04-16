package prompt

import "go.uber.org/fx"

var Module = fx.Module("prompt",
	fx.Provide(
		NewConfig,
		NewService,
		AsPromptRegistry,
		AsPromptAssemblyService,
		AsDynamicSectionRegistrar,
		AsSectionInvalidator,
	),
)
