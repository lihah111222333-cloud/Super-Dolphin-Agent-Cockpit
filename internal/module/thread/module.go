// Package thread 管理 agent thread 的完整生命周期：创建、恢复、fork、归档、删除，
// 以及与 provider session、binding store、prompt assembly 之间的协调。
package thread

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/threadprompt"
	promptstore "github.com/anthropic-ai/super-agent-v3/internal/store/prompt"
	"go.uber.org/fx"
)

var _ contract.PendingLaunchSpawner = (Service)(nil)

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

// provideThreadConcreteOutputs 从 Service 接口提取具体类型 *service 和 ThreadStateConfigReader。
func provideThreadConcreteOutputs(svc Service) (*service, contract.ThreadStateConfigReader) {
	concrete, _ := svc.(*service)
	return concrete, concrete
}

// provideCronThreadStarter 将 Service 适配为 contract.CronThreadStarter，供 cron 模块调用。
func provideCronThreadStarter(svc Service) contract.CronThreadStarter {
	return NewCronStarterAdapter(svc)
}

// threadPromptProviderParams 是 registerThreadPromptProviders 的 fx 注入参数聚合。
type threadPromptProviderParams struct {
	fx.In
	Registrar     contract.DynamicSectionRegistrar `optional:"true"`
	PromptStore   promptstore.Store                `optional:"true"`
	Builtin       contract.BuiltinPromptRegistry   `optional:"true"`
	PromptCatalog promptstore.RuntimePromptCatalog `optional:"true"`
}

// registerThreadPromptProviders 向 prompt section registrar 注册 thread 相关的 prompt provider。
func registerThreadPromptProviders(params threadPromptProviderParams) error {
	catalog := params.PromptCatalog
	if catalog == nil {
		catalog = threadprompt.NewRuntimeCatalog(params.PromptStore, params.Builtin)
	}
	return threadprompt.RegisterProviders(params.Registrar, catalog)
}

// runtimePromptCatalogParams 是 provideRuntimePromptCatalog 的 fx 注入参数聚合。
type runtimePromptCatalogParams struct {
	fx.In
	PromptStore promptstore.Store              `optional:"true"`
	Builtin     contract.BuiltinPromptRegistry `optional:"true"`
}

// provideRuntimePromptCatalog 构建运行时 prompt catalog，供路由和 provider 注入使用。
func provideRuntimePromptCatalog(params runtimePromptCatalogParams) promptstore.RuntimePromptCatalog {
	return threadprompt.NewRuntimeCatalog(params.PromptStore, params.Builtin)
}
