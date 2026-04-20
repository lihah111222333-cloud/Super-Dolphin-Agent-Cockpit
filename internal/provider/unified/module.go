package unified

import (
	"context"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
)

type RegistryParams struct {
	fx.In

	Drivers    []contract.DriverFactory                `group:"drivers"`
	SkillPorts []contract.SkillInjectionPortDescriptor `group:"skill_injection_descriptors"`
}

type dreamExecutorParams struct {
	fx.In

	Providers []contract.DreamExecutorProvider `group:"dream_executors"`
}

var Module = fx.Module("provider.unified",
	fx.Provide(
		NewEventDispatcher,
		NewRegistry,
		provideSkillInjectionResolver,
		fx.Annotate(NewClient, fx.As(new(thread.SessionStarter))),
		NewSessionManager,
		fx.Annotate(NewSessionProvider, fx.As(new(thread.SessionProvider))),
		NewTurnSessionProvider,
		fx.Annotate(NewSessionCleaner, fx.As(new(contract.OrchestrationSessionCleaner))),
		NewSessionResolver,
		provideDreamExecutor,
	),
	fx.Invoke(registerSessionShutdown),
)

func provideDreamExecutor(p dreamExecutorParams) contract.DreamExecutor {
	return NewDreamExecutor(p.Providers)
}

func provideSkillInjectionResolver(registry *Registry) contract.SkillInjectionPortResolver {
	return registry
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
