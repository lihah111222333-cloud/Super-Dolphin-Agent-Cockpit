package thread

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	"go.uber.org/fx"
)

var _ contract.PendingLaunchSpawner = (Service)(nil)

// Module wires thread services, subscribers, prompt providers, and RPC handlers.
var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndSharedFiles,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			fx.As(new(Service)),
			fx.As(new(contract.PendingLaunchSpawner)),
		),
		fx.Annotate(NewThreadHandlers, fx.ParamTags("", `optional:"true"`)),
		provideThreadConcreteOutputs,
		provideRuntimePromptCatalog,
		NewThreadSubscribers,
		provideCronThreadStarter,
	),
	fx.Provide(fx.Annotate(threadBusWorkersAsRunner, fx.ResultTags(`group:"runners"`))),
	fx.Provide(NewBindingRecoveryReporter, NewThreadLister, NewThreadConfigReader, NewThreadRuntimeConfigReader),
	fx.Invoke(registerThreadPromptProviders),
)

func provideThreadConcreteOutputs(svc Service) (*service, contract.ThreadStateConfigReader) {
	concrete, _ := svc.(*service)
	return concrete, concrete
}

func provideCronThreadStarter(svc Service) contract.CronThreadStarter {
	return NewCronStarterAdapter(svc)
}

type threadPromptProviderParams struct {
	fx.In
	Registrar     contract.DynamicSectionRegistrar `optional:"true"`
	PromptStore   contract.PromptStore             `optional:"true"`
	Builtin       contract.BuiltinPromptRegistry   `optional:"true"`
	PromptCatalog contract.RuntimePromptCatalog    `optional:"true"`
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
	PromptStore contract.PromptStore           `optional:"true"`
	Builtin     contract.BuiltinPromptRegistry `optional:"true"`
}

func provideRuntimePromptCatalog(params runtimePromptCatalogParams) contract.RuntimePromptCatalog {
	return threadprompt.NewRuntimeCatalog(params.PromptStore, params.Builtin)
}
