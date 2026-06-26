package unified

import (
	"context"
	"log/slog"

	"go.uber.org/fx"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
)

// RegistryParams 收集所有 provider driver factory，供统一 registry 构建查找表。
type RegistryParams struct {
	fx.In

	Drivers []contract.DriverFactory `group:"drivers"`
}

// clientParams 是 unified client 的 Fx 入参，logger 和 tracer 允许按运行环境可选注入。
type clientParams struct {
	fx.In
	Registry *Registry
	Sessions *SessionManager
	Logger   *slog.Logger           `optional:"true"`
	Tracer   *observability.Service `optional:"true"`
}

// dreamExecutorParams 收集 dream executor provider，供 dream failover 调度器使用。
type dreamExecutorParams struct {
	fx.In

	Providers []contract.DreamExecutorProvider `group:"dream_executors"`
	Logger    *slog.Logger                     `optional:"true"`
}

// sessionResolverParams 组合内存 session、thread store 和 binding store，支撑跨模块 session 恢复。
type sessionResolverParams struct {
	fx.In

	ThreadStore   contract.SessionThreadLookup    `optional:"true"`
	BindingStore  contract.SessionBindingLookup   `optional:"true"`
	BindingWriter contract.SessionBindingUpserter `optional:"true"`
	Registry      *Registry
	Sessions      *SessionManager
}

// Module 注册 unified provider 层的 registry、session 管理、事件分发和 dream 调度组件。
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

// provideClient 用 Fx 入参创建统一 provider client，避免构造细节泄漏给模块装配层。
func provideClient(p clientParams) *Client {
	return newClient(p.Registry, p.Sessions, p.Logger, p.Tracer)
}

// provideDreamExecutor 把 provider 分组收窄为跨模块 DreamExecutor 接口，并把配置错误交给 Fx 阻断启动。
func provideDreamExecutor(p dreamExecutorParams) (contract.DreamExecutor, error) {
	return newDreamExecutor(p.Providers, p.Logger)
}

// registerSessionShutdown 在 Fx 停止阶段关闭全部 provider session，防止后台进程泄漏。
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
