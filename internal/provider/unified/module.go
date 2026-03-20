package unified

import (
	"context"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/orchestration"
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
		fx.Annotate(NewClient, fx.As(new(thread.SessionStarter))),
		NewSessionManager,
		fx.Annotate(NewSessionProvider, fx.As(new(thread.SessionProvider))),
		fx.Annotate(NewSessionCleaner, fx.As(new(orchestration.SessionCleaner))),
		NewSessionResolver,
	),
	fx.Invoke(registerSessionShutdown),
)

func registerSessionShutdown(lc fx.Lifecycle, sessions *SessionManager) {
	if sessions == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			sessions.CloseAll(ctx)
			return nil
		},
	})
}
