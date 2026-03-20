package app

import (
	"context"
	"log/slog"
	"os"

	"go.uber.org/fx"

	uiwails "github.com/anthropic-ai/super-agent-v3/internal/ui/wails"
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
	return runApp(newFXApp(uiwails.Module))
}

func newFXApp(options ...fx.Option) *fx.App {
	base := []fx.Option{Module}
	base = append(base, options...)
	base = append(base, fx.Invoke(BindRuntime))
	return fx.New(base...)
}

func runApp(app *fx.App) error {
	if err := app.Start(context.Background()); err != nil {
		return err
	}
	<-app.Done()
	return app.Stop(context.Background())
}
