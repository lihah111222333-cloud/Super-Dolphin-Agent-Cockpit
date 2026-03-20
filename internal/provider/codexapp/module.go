package codexapp

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func NewDriverFactory(logger *slog.Logger, dispatcher *unified.EventDispatcher) contract.DriverFactory {
	return contract.DriverFactory{
		Name: "codex",
		Create: func() contract.Driver {
			return newDriver(logger, dispatcher)
		},
	}
}

var Module = fx.Module("provider.codexapp",
	fx.Provide(
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
	),
	fx.Invoke(RegisterTranslators),
)
