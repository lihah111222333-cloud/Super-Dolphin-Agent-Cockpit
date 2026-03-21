package app

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"go.uber.org/fx"

	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
	"github.com/wailsapp/wails/v3/pkg/application"
)

func NewLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

func NewApp() *fx.App {
	return newFXApp()
}

func Run() error {
	return runApp(NewApp())
}

func RunDesktop() error {
	ctx := context.Background()
	var wailsApp *application.App
	var lifecycle *uiwails.WailsLifecycle

	app := newDesktopFXApp(fx.Populate(&wailsApp, &lifecycle))
	if err := app.Start(ctx); err != nil {
		return err
	}
	if wailsApp == nil {
		return errors.New("wails application not available")
	}
	if lifecycle == nil {
		return errors.New("wails lifecycle not available")
	}

	stopWatch := watchFXShutdown(app, lifecycle)
	runErr := wailsApp.Run()
	close(stopWatch)

	stopErr := stopFXApp(ctx, app)
	return errors.Join(runErr, stopErr)
}

func newFXApp(options ...fx.Option) *fx.App {
	base := []fx.Option{
		Module,
		fx.Invoke(BindRuntime),
		fx.StopTimeout(platformconfig.ShutdownTimeout),
	}
	base = append(base, options...)
	return fx.New(base...)
}

func runApp(app *fx.App) error {
	if err := app.Start(context.Background()); err != nil {
		return err
	}
	<-app.Done()
	return stopFXApp(context.Background(), app)
}

func newDesktopFXApp(options ...fx.Option) *fx.App {
	base := []fx.Option{
		Module,
		uiwails.Module,
		fx.Invoke(BindRuntime),
		fx.StopTimeout(platformconfig.ShutdownTimeout),
	}
	base = append(base, options...)
	return fx.New(base...)
}

func stopFXApp(parent context.Context, app *fx.App) error {
	if parent == nil {
		parent = context.Background()
	}
	// Keep the caller-side hard stop aligned with fx.StopTimeout so shutdown
	// cannot hang indefinitely outside the lifecycle hook deadline.
	ctx, cancel := context.WithTimeout(parent, platformconfig.ShutdownTimeout)
	defer cancel()
	return app.Stop(ctx)
}

func watchFXShutdown(app *fx.App, lifecycle *uiwails.WailsLifecycle) chan struct{} {
	stop := make(chan struct{})
	go func() {
		select {
		case <-app.Done():
			lifecycle.NotifyBackendFailed()
		case <-stop:
		}
	}()
	return stop
}
