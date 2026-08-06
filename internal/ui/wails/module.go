package wails

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/appupdate"
	datasourcev2 "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/datasource_v2"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
	platformmetrics "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/metrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/cronmetrics"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
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
		NewActivationReadiness,
		NewEventBridge,
		NewWailsApplication,
		NewHTTPAssetServer,
		provideAppUpdateRequestQuit,
		provideDatasourceImportPickerTokenVerifier,
	),
	fx.Invoke(bindEventBridge),
)

// AppParams 汇总创建 App 绑定所需的跨模块依赖。
// 导出该装配参数后，外部桌面宿主仍可复用 production App binding，而无需复制 dispatch 逻辑。
type AppParams struct {
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
// 它装配 RPC dispatch、runtime event 推送、观测依赖和当前桌面实例的资源 capability 状态。
func NewApp(p AppParams) *App {
	return &App{
		dispatch: p.Dispatcher.Dispatch,
		emitter:  func(string, any) {},
		pushRuntimeEvent: func(ctx context.Context, event string, payload any) {
			p.RPCServer.NotifyAll(ctx, p.PushBridge, event, payload)
		},
		windowTitle:                  applicationTitle(),
		debug:                        isDebug(p.Config),
		observability:                p.Observability,
		localImageAssetRegistry:      newLocalImageAssetRegistry(time.Now),
		sharedFilePreviewRegistry:    newSharedFilePreviewRegistry(time.Now),
		sharedFilePreviewHTTPAddr:    &sharedFilePreviewHTTPAddr{},
		datasourceImportPickerTokens: newDatasourceImportPickerTokens(nil),
	}
}

// NewService 把 App 绑定包装为 Wails application.Service。
func NewService(app *App) application.Service {
	return application.NewService(app)
}

// provideDatasourceImportPickerTokenVerifier 只把桌面 App 暴露为 datasource 本地导入 capability 验证器。
// token 状态仍保存在 Wails 层，业务模块只能调用验证接口，不能自行签发。
func provideDatasourceImportPickerTokenVerifier(app *App) datasourcev2.LocalFilePickerTokenVerifier {
	return app
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

// ApplicationParams 汇总创建 Wails application 所需依赖。
// 测试桌面宿主使用同一构造器装配真实 Wails application 与 lifecycle emitter。
type ApplicationParams struct {
	fx.In

	Logger    *slog.Logger
	Binding   *App
	Service   application.Service
	Lifecycle *WailsLifecycle
	Readiness *ActivationReadiness
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
	Metrics          *cronmetrics.Metrics
	DAGMetrics       *platformmetrics.DAGCollector
	BootstrapMetrics *platformmetrics.BootstrapMetrics
	SkillMetrics     *skillmetrics.Registry
	Binding          *App
}

// NewWailsApplication 创建 Wails 桌面应用。
// 窗口标题和调试开关来自绑定对象，避免应用层重复解析桌面配置。
func NewWailsApplication(p ApplicationParams) (*application.App, error) {
	if p.Readiness == nil {
		return nil, errors.New("wails activation readiness is required")
	}
	if p.Binding == nil {
		return nil, errors.New("wails app binding is required")
	}
	if p.Lifecycle == nil {
		return nil, errors.New("wails lifecycle is required")
	}
	localAssets, err := p.Binding.localImageAssets()
	if err != nil {
		return nil, err
	}
	sharedAssets, _, err := p.Binding.sharedFilePreviewAssets()
	if err != nil {
		return nil, err
	}
	title := applicationTitle()
	debug := false
	if value := strings.TrimSpace(p.Binding.windowTitle); value != "" {
		title = value
	}
	debug = p.Binding.debug
	assetHandler, err := assetHandlerFromForMode(p.Frontend, debug)
	if err != nil {
		return nil, err
	}
	if err := p.Binding.bindFrontendReadiness(p.Readiness, p.Lifecycle); err != nil {
		return nil, err
	}
	wailsApp := application.New(application.Options{
		Name:                        title,
		Description:                 "Super Dolphin desktop",
		Logger:                      p.Logger,
		Services:                    []application.Service{p.Service},
		DisableDefaultSignalHandler: true,
		Assets: application.AssetOptions{
			Handler: withSharedFilePreviewAssetsRegistry(withClipboardAssetsRegistry(assetHandler, localAssets), sharedAssets),
		},
		ShouldQuit: p.Lifecycle.ShouldQuit,
		OnShutdown: p.Lifecycle.OnShutdown,
		Mac: application.MacOptions{
			ApplicationShouldTerminateAfterLastWindowClosed: true,
		},
	})
	p.Binding.bindRuntime(wailsApp)
	p.Lifecycle.SetQuitFunc(wailsApp.Quit)
	p.Lifecycle.SetEventEmitter(p.Binding.emitRuntimeEvent)
	wailsApp.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		p.Readiness.MarkApplicationStarted()
		safego.Go(context.Background(), nil, "wails.clipboard.cleanup", func(context.Context) {
			cleanupStaleClipboardImages(p.Logger, os.TempDir(), defaultClipboardRetention)
		})
	})
	createWindow(wailsApp, title, debug, "main", "", "", p.Binding)
	return wailsApp, nil
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

// bindEventBridge 将 EventBridge 挂到 Fx 生命周期。
func bindEventBridge(lc fx.Lifecycle, bridge *EventBridge) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			return bridge.Start()
		},
		OnStop: func(context.Context) error {
			bridge.Stop()
			return nil
		},
	})
}
