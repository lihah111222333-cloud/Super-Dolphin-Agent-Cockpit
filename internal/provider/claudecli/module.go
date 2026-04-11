package claudecli

import (
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
)

func NewDriverFactory(logger *slog.Logger, dispatcher *unified.EventDispatcher, reporter contract.RuntimeReporter, reg *pidregistry.Registry) contract.DriverFactory {
	return contract.DriverFactory{
		Name: "claude",
		Create: func() contract.Driver {
			return newDriver(logger, dispatcher, reporter, reg)
		},
	}
}

var Module = fx.Module("provider.claudecli",
	fx.Provide(
		fx.Annotate(NewDriverFactory, fx.ResultTags(`group:"drivers"`)),
	),
	fx.Invoke(RegisterTranslators),
)
