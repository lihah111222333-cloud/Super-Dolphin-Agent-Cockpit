package thread

import (
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"go.uber.org/fx"
)

// Compile-time check: thread.Service must satisfy
// contract.PendingLaunchSpawner so the fx graph below can publish it
// under that interface directly. P22 P4 S2 moved the interface from the
// turn consumer package to internal/contract so the owner-side contract
// stops leaking through a side-channel interface; the thread module no
// longer needs to import turn just to declare what shape it satisfies.
var _ contract.PendingLaunchSpawner = (Service)(nil)

var Module = fx.Module("thread",
	fx.Provide(
		fx.Annotate(
			NewServiceWithPromptAssemblyAndSharedFiles,
			fx.ParamTags("", `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`, `optional:"true"`),
			// Publish the service under both thread.Service (its native
			// interface, required by uistate/orchestration) and
			// contract.PendingLaunchSpawner so NewTurnHandlers can pick it
			// up via optional injection without creating a turn→thread
			// import cycle. fx.As replaces the original output, so we need
			// an explicit fx.As(new(Service)) here, otherwise thread.Service
			// disappears from the DI graph. P22 P4 S2: the second fx.As
			// target used to be turn.PendingLaunchSpawner (side-channel
			// interface owned by the consumer package); it now lives in
			// contract.
			fx.As(new(Service)),
			fx.As(new(contract.PendingLaunchSpawner)),
		),
		fx.Annotate(
			NewThreadHandlers,
			fx.ParamTags("", `optional:"true"`),
		),
		provideThreadConcreteService,
		NewThreadSubscribers,
		// Publish the narrow CronThreadStarter adapter so the cron module
		// can bootstrap threads without importing internal/module/thread.
		provideCronThreadStarter,
	),
	fx.Provide(
		fx.Annotate(threadBusWorkersAsRunner, fx.ResultTags(`group:"runners"`)),
	),
	// Publish narrow contract adapters so downstream consumers (uistate)
	// can depend on contract interfaces instead of thread.Service directly.
	fx.Provide(NewThreadLister),
	fx.Provide(NewThreadConfigReader),
	fx.Provide(NewThreadRuntimeConfigReader),
)

func provideThreadConcreteService(svc Service) *service {
	concrete, _ := svc.(*service)
	return concrete
}

// provideCronThreadStarter wraps thread.Service in a narrow adapter so
// the cron module can start threads via contract.CronThreadStarter
// without importing this package.
func provideCronThreadStarter(svc Service) contract.CronThreadStarter {
	return NewCronStarterAdapter(svc)
}
