package thread

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"go.uber.org/fx"
)

// Keep the owner-side launch contract in internal/contract, not turn.
var _ contract.PendingLaunchSpawner = (Service)(nil)

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndSharedFiles,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			// fx.As replaces the original output, so publish both native
			// thread.Service and the narrow cross-module launch contract.
			fx.As(new(Service)),
			fx.As(new(contract.PendingLaunchSpawner)),
		),
		fx.Annotate(
			NewThreadHandlers,
			fx.ParamTags("", `optional:"true"`),
		),
		provideThreadConcreteService,
		provideRuntimePromptCatalog,
		NewThreadSubscribers,
		// Let cron start threads without importing internal/module/thread.
		provideCronThreadStarter,
	),
	fx.Provide(
		fx.Annotate(threadBusWorkersAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	fx.Provide(NewBindingRecoveryReporter),
	fx.Provide(NewThreadLister),
	fx.Provide(NewThreadConfigReader),
	fx.Provide(NewThreadRuntimeConfigReader),
	fx.Provide(NewThreadStateRuntimeConfigReader),
	fx.Invoke(registerThreadPromptProviders),
)

func provideThreadConcreteService(svc Service) *service {
	concrete, _ := svc.(*service)
	return concrete
}

func provideCronThreadStarter(svc Service) contract.CronThreadStarter {
	return NewCronStarterAdapter(svc)
}

type threadPromptProviderParams struct {
	fx.In

	Registrar     contract.DynamicSectionRegistrar `optional:"true"`
	PromptStore   promptstore.Store                `optional:"true"`
	Builtin       contract.BuiltinPromptRegistry   `optional:"true"`
	PromptCatalog promptstore.RuntimePromptCatalog `optional:"true"`
}

func registerThreadPromptProviders(params threadPromptProviderParams) error {
	catalog := params.PromptCatalog
	if catalog == nil {
		catalog = threadprompt.NewRuntimeCatalog(params.PromptStore, params.Builtin)
	}
	return threadprompt.RegisterProviders(params.Registrar, catalog)
}

type runtimePromptCatalogParams struct {
	fx.In

	PromptStore promptstore.Store              `optional:"true"`
	Builtin     contract.BuiltinPromptRegistry `optional:"true"`
}

func provideRuntimePromptCatalog(params runtimePromptCatalogParams) promptstore.RuntimePromptCatalog {
	return threadprompt.NewRuntimeCatalog(params.PromptStore, params.Builtin)
}
