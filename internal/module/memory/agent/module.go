package agent

import "go.uber.org/fx"

var Module = fx.Module("memory-agent",
	fx.Provide(
		NewManager,
		NewPromptProvider,
	),
)
