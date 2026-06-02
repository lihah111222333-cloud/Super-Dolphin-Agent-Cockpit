package wails

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"go.uber.org/fx"
)

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
	),
	fx.Invoke(bindWailsLifecycle),
	fx.Invoke(bindEventBridge),
)

func NewApp(dispatcher contract.RPCDispatcher, cfg *config.Config) *App {
	return &App{
		dispatch:    dispatcher.Dispatch,
		emitter:     func(string, any) {},
		windowTitle: applicationTitle(),
		debug:       isDebug(cfg),
	}
}

func NewService(app *App) application.Service {
	return application.NewService(app)
}

type activeAgentCounterParams struct {
	fx.In

	Threads contract.ThreadLister
}

func NewActiveAgentCounter(p activeAgentCounterParams) ActiveAgentCounter {
	if p.Threads != nil {
		return ActiveAgentCounterFunc(func(ctx context.Context) (int, error) {
			threads, err := p.Threads.List(ctx)
			if err != nil {
				return 0, err
			}
			active := 0
			for _, thread := range threads {
				if contract.IsActiveAgentState(thread.Status) {
					active++
				}
			}
			return active, nil
		})
	}
	return ActiveAgentCounterFunc(func(context.Context) (int, error) {
		return 0, errors.New("active agent source is not configured")
	})
}

type applicationParams struct {
	fx.In

	Logger    *slog.Logger
	Binding   *App
	Service   application.Service
	Lifecycle *WailsLifecycle
	Frontend  FrontendFS `optional:"true"`
}

// httpAssetRunnerResult mirrors app.RunnerResult to avoid an import cycle.
type httpAssetRunnerResult struct {
	fx.Out
	Runner platformrunner.Runner `group:"runners"`
}

type httpAssetServerParams struct {
	fx.In

	Logger   *slog.Logger
	Frontend FrontendFS `optional:"true"`
	Config   *config.Config
	Server   *rpc.Server
}

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
		Description: "Super Agent desktop",
		Logger:      p.Logger,
		Services:    []application.Service{p.Service},
		Assets: application.AssetOptions{
			Handler: withClipboardAssets(AssetHandlerFrom(p.Frontend)),
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

func applicationTitle() string {
	return "Super Agent"
}

func isDebug(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(cfg.LogLevel), "debug")
}

func bindWailsLifecycle(lifecycle *WailsLifecycle, shutdowner fx.Shutdowner, logger *slog.Logger) {
	if lifecycle == nil {
		return
	}
	lifecycle.SetShutdownerFunc(func() {
		shared.LogIgnoredError(logger, "shutdown failed", shutdowner.Shutdown())
	})
}

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
