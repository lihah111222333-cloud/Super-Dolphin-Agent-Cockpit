package wails

import (
	"context"
	"log/slog"
	"strings"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	"github.com/anthropic-ai/super-agent-v3/internal/module/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"go.uber.org/fx"
)

var Module = fx.Module("ui.wails",
	fx.Provide(
		NewApp,
		NewService,
		NewActiveAgentCounter,
		NewWailsLifecycle,
		NewEventBridge,
		NewWailsApplication,
	),
	fx.Invoke(bindWailsLifecycle),
	fx.Invoke(bindEventBridge),
)

func NewApp(server *rpc.Server) *App {
	return &App{
		dispatch: server.Dispatch,
		emitter:  func(string, any) {},
	}
}

func NewService(app *App) application.Service {
	return application.NewService(app)
}

func NewActiveAgentCounter(svc orchestration.Service) ActiveAgentCounter {
	if svc == nil {
		return ActiveAgentCounterFunc(func(context.Context) (int, error) {
			return 0, nil
		})
	}
	return ActiveAgentCounterFunc(func(ctx context.Context) (int, error) {
		snapshots, err := svc.ListAgents(ctx)
		if err != nil {
			return 0, err
		}
		active := 0
		for _, snapshot := range snapshots {
			if isActiveAgentState(snapshot.State) {
				active++
			}
		}
		return active, nil
	})
}

type applicationParams struct {
	fx.In

	Logger    *slog.Logger
	Config    *config.Config
	Binding   *App
	Service   application.Service
	Lifecycle *WailsLifecycle
}

func NewWailsApplication(p applicationParams) *application.App {
	title := applicationTitle()
	wailsApp := application.New(application.Options{
		Name:        title,
		Description: "Super Agent desktop",
		Logger:      p.Logger,
		Services:    []application.Service{p.Service},
		Assets: application.AssetOptions{
			Handler: AssetHandler(),
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
	})
	CreateMainWindow(wailsApp, title, isDebug(p.Config))
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

func bindWailsLifecycle(lifecycle *WailsLifecycle, shutdowner fx.Shutdowner) {
	if lifecycle == nil {
		return
	}
	lifecycle.SetShutdownerFunc(func() {
		_ = shutdowner.Shutdown()
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

func isActiveAgentState(state string) bool {
	switch state {
	case "", agentdto.StateStopped, agentdto.StateFailed:
		return false
	default:
		return true
	}
}
