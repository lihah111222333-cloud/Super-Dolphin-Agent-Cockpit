package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

func TestParentFxStartup(t *testing.T) {
	// P22 P4 S4c1: orchestration package no longer exports `Module`;
	// root assembly composes the wiring from the exported building
	// blocks. Test mirrors the production composition in
	// buildOrchestrationOptions (cmd/mcp-orch/fx.go).
	orchAssembly := fx.Module("orchestration",
		fx.Provide(
			orchestration.ProvideService,
			orchestration.ProvideServiceInterface,
			orchestration.ProvideHookAfterHandler,
			orchestration.ProvideRPCFacade,
		),
		fx.Invoke(orchestration.RegisterTurnLifecycle),
		fx.Invoke(orchestration.RegisterApprovalLifecycle),
		fx.Provide(fx.Annotate(orchestration.ProvideWakeupDispatcherRunner, fx.ResultTags(`group:"runners"`))),
		fx.Provide(fx.Annotate(orchestration.ProvideWakeupReclaimerRunner, fx.ResultTags(`group:"runners"`))),
	)
	type consumeRunners struct {
		fx.In
		Runners []platformrunner.Runner `group:"runners"`
	}
	app := fx.New(fx.NopLogger, orchAssembly, fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil))), fx.Supply(event.NewDispatcher()), fx.Provide(newNoopSessionCleaner, newNoopTurnStarter, func(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
		return orchestration.NewLocalLauncher(turnStarter, logger)
	}), fx.Invoke(func(consumeRunners) {}))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
}
