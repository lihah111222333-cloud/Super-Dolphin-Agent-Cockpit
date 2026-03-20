package app

import (
	"context"
	"log/slog"
	"os"

	"go.uber.org/fx"
)

func NewLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
}

func NewApp() *fx.App {
	return fx.New(
		Module,
		fx.Invoke(BindRuntime),
	)
}

func Run() error {
	app := NewApp()
	if err := app.Start(context.Background()); err != nil {
		return err
	}
	<-app.Done()
	return app.Stop(context.Background())
}
