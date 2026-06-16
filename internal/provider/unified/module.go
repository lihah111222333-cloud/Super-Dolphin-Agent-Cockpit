package unified

import (
	"context"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

type RegistryParams struct {
	fx.In

	Drivers []contract.DriverFactory `group:"drivers"`
}

type clientParams struct {
	fx.In
	Registry *Registry
	Sessions *SessionManager
	Logger   *pkglogger.Logger      `optional:"true"`
	Tracer   *observability.Service `optional:"true"`
}

type dreamExecutorParams struct {
	fx.In

	Providers []contract.DreamExecutorProvider `group:"dream_executors"`
	Logger    *pkglogger.Logger                `optional:"true"`
}

type sessionResolverParams struct {
	fx.In

	ThreadStore   contract.SessionThreadLookup    `optional:"true"`
	BindingStore  contract.SessionBindingLookup   `optional:"true"`
	BindingWriter contract.SessionBindingUpserter `optional:"true"`
	Registry      *Registry
	Sessions      *SessionManager
}

var Module = fx.Module("provider.unified",
	fx.Provide(
		NewEventDispatcher,
		NewRegistry,
		fx.Annotate(provideClient, fx.As(new(contract.SessionStarter))),
		NewSessionManager,
		fx.Annotate(NewSessionProvider, fx.As(new(contract.SessionProvider))),
		NewTurnSessionProvider,
		fx.Annotate(NewSessionCleaner, fx.As(new(contract.OrchestrationSessionCleaner))),
		NewSessionResolver,
		provideDreamExecutor,
	),
	fx.Invoke(registerSessionShutdown),
)

func provideClient(p clientParams) *Client {
	return newClient(p.Registry, p.Sessions, p.Logger, p.Tracer)
}

func provideDreamExecutor(p dreamExecutorParams) contract.DreamExecutor {
	return NewDreamExecutor(p.Providers, p.Logger)
}

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
