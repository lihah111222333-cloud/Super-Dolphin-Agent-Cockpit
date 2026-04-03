package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

func TestParentFxStartup(t *testing.T) {
	app := fx.New(fx.NopLogger, orchestration.Module, fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil))), fx.Supply(event.NewDispatcher()), fx.Provide(newNoopSessionCleaner, newNoopTurnStarter, func(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
		return orchestration.NewLocalLauncher(turnStarter, logger)
	}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
}
