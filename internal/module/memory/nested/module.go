package nested

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

var Module = fx.Module("memory.nested",
	fx.Provide(
		NewNestedRuntime,
		fx.Annotate(
			NewClaudeMdSourcesProvider,
			fx.As(new(contract.ClaudeMdSourceProvider)),
		),
	),
)
