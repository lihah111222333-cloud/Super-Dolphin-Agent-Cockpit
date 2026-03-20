package unified

import (
	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
)

type RegistryParams struct {
	fx.In

	Drivers []contract.DriverFactory `group:"drivers"`
}

var Module = fx.Module("provider.unified",
	fx.Provide(
		NewEventDispatcher,
		NewRegistry,
		NewClient,
		NewSessionManager,
		fx.Annotate(NewSessionProvider, fx.As(new(thread.SessionProvider))),
	),
)
