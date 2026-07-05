package wails

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/appupdate"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"go.uber.org/fx"
)

// Module 注册 Wails 桌面 UI、RPC handler、资源服务和生命周期桥接。
var Module = fx.Module("ui.wails",
	fx.Provide(
		NewApp,
		NewRPCHandlers,
		NewService,
		NewActiveAgentCounter,
		NewWailsLifecycle,
		NewEventBridge,
		NewWailsApplication,
		NewHTTPAssetServer,
		provideAppUpdateRequestQuit,
	),
	fx.Invoke(bindWailsLifecycle),
	fx.Invoke(bindEventBridge),
)

// appParams 汇总创建 App 绑定所需的跨模块依赖。
type appParams struct {
	fx.In

	Dispatcher    contract.RPCDispatcher
	Config        *config.Config
	Observability *observability.Service `optional:"true"`
	RPCServer     *rpc.Server
	PushBridge    *rpc.PushBridge
}

// provideAppUpdateRequestQuit 把 WailsLifecycle 的退出请求暴露给 app update 模块。
func provideAppUpdateRequestQuit(lifecycle *WailsLifecycle) appupdate.RequestQuit {
	return lifecycle.RequestQuit
}

// NewApp 创建暴露给 Wails 前端的后端绑定对象。
// 它只装配 RPC dispatch、runtime event 推送和观测依赖，不持有业务模块状态。
func NewApp(p appParams) *App {
	return &App{
		dispatch: p.Dispatcher.Dispatch,
		emitter:  func(string, any) {},
		pushRuntimeEvent: func(ctx context.Context, event string, payload any) {
			p.RPCServer.NotifyAll(ctx, p.PushBridge, event, payload)
		},
		windowTitle:   applicationTitle(),
		debug:         isDebug(p.Config),
		observability: p.Observability,
	}
}

// NewService 把 App 绑定包装为 Wails application.Service。
func NewService(app *App) application.Service {
	return application.NewService(app)
}

// activeAgentCounterParams 汇总创建活跃 agent 计数器所需依赖。
type activeAgentCounterParams struct {
	fx.In

	Threads contract.ThreadLister
}

// NewActiveAgentCounter 创建活跃 agent 计数器；缺失线程来源时 fail-fast 返回错误。
func NewActiveAgentCounter(p activeAgentCounterParams) ActiveAgentCounter {
	if p.Threads != nil {
		return ActiveAgentCounterFunc(func(ctx context.Context) (int, error) {
			counter, ok := p.Threads.(contract.ThreadActiveCounter)
			if !ok {
				return 0, errors.New("active agent count source is not configured")
			}
			if !contract.IsActiveAgentState("created") {
				return 0, errors.New("active agent state predicate rejected created state")
			}
			count, err := counter.CountActive(ctx)
			if err != nil {
				return 0, err
			}
			if count < 0 {
				return 0, errors.New("active agent count source returned negative count")
			}
			maxInt := int64(^uint(0) >> 1)
			if count > maxInt {
				return 0, errors.New("active agent count exceeds int range")
			}
			return int(count), nil
		})
	}
	return ActiveAgentCounterFunc(func(context.Context) (int, error) {
		return 0, errors.New("active agent source is not configured")
	})
}

// applicationParams 汇总创建 Wails application 所需依赖。
type applicationParams struct {
	fx.In

	Logger    *slog.Logger
	Binding   *App
	Service   application.Service
	Lifecycle *WailsLifecycle
	Frontend  FrontendFS `optional:"true"`
}

// httpAssetRunnerResult 镜像 app.RunnerResult，避免 ui/wails 反向导入 app 包。
type httpAssetRunnerResult struct {
	fx.Out
	Runner platformrunner.Runner `group:"runners"`
}

// httpAssetServerParams 汇总 HTTP asset server 运行所需依赖。
type httpAssetServerParams struct {
	fx.In

	Logger   *slog.Logger
	Frontend FrontendFS `optional:"true"`
	Config   *config.Config
	Server   *rpc.Server
}

// NewWailsApplication 创建 Wails 桌面应用。
// 窗口标题和调试开关来自绑定对象，避免应用层重复解析桌面配置。
func NewWailsApplication(p applicationParams) *application.App {
	title := applicationTitle()
	debug := false
	if p.Binding != nil {
		if value := strings.TrimSpace(p.Binding.windowTitle); value != "" {
			title = value
		}
		debug = p.Binding.debug
	}
	wailsApp := application.New(application.Options{
		Name:        title,
		Description: "Super Dolphin desktop",
		Logger:      p.Logger,
		Services:    []application.Service{p.Service},
		Assets: application.AssetOptions{
			Handler: withClipboardAssets(AssetHandlerFromForMode(p.Frontend, debug)),
		},
		ShouldQuit: p.Lifecycle.ShouldQuit,
		OnShutdown: p.Lifecycle.OnShutdown,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	p.Binding.bindRuntime(wailsApp)
	p.Lifecycle.SetQuitFunc(wailsApp.Quit)
	p.Lifecycle.SetEventEmitter(func(channel string, payload any) {
		wailsApp.Event.Emit(channel, payload)
	})
	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		p.Lifecycle.MarkFrontendReady()
		go cleanupStaleClipboardImages(p.Logger, os.TempDir(), defaultClipboardRetention)
	})
	createWindow(wailsApp, title, debug, "main", "", "", p.Binding)
	return wailsApp
}

// applicationTitle 返回桌面应用标题。
func applicationTitle() string {
	return "Super Dolphin"
}

// isDebug 根据配置判断 Wails 是否启用调试模式。
func isDebug(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cfg.LogLevel), "debug")
}

// bindWailsLifecycle 把 Fx shutdowner 绑定到 Wails 退出流程。
func bindWailsLifecycle(lifecycle *WailsLifecycle, shutdowner fx.Shutdowner, logger *slog.Logger) {
	if lifecycle == nil {
		return
	}
	lifecycle.SetShutdownerFunc(func() {
		shared.LogIgnoredError(logger, "shutdown failed", shutdowner.Shutdown())
	})
}

// bindEventBridge 将 EventBridge 挂到 Fx 生命周期。
func bindEventBridge(lc fx.Lifecycle, bridge *EventBridge) {
	if bridge == nil {
		return
	}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			bridge.Start()
			return nil
		},
		OnStop: func(context.Context) error {
			bridge.Stop()
			return nil
		},
	})
}
