package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	taskdagstore "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/kelindar/event"
	"go.uber.org/fx"
)

// stubRunStore / stubDAGStore / stubAgentThreadStore / stubAgentBindingStore
// are nil-embedding stubs used to satisfy fx wiring assertions only; they
// are never actually invoked. 余下 method 仅由 fx 装配需要，不被调用。
type stubRunStore struct{ taskdagstore.RunStore }
type stubDAGStore struct {
	taskdagstore.OrchestrationStore
}
type stubAgentThreadStore struct{ orchestration.AgentThreadStore }
type stubAgentBindingStore struct {
	orchestration.AgentBindingStore
}

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
		// dispatcher-wiring batch §1 重构后：Runner provider 消费
		// *WakeupDispatcher 单例，需同时提供 ProvideWakeupDispatcher。
		fx.Provide(orchestration.ProvideWakeupDispatcher),
		fx.Provide(fx.Annotate(orchestration.ProvideWakeupDispatcherRunner, fx.ResultTags(`group:"runners"`))),
		fx.Provide(fx.Annotate(orchestration.ProvideWakeupReclaimerRunner, fx.ResultTags(`group:"runners"`))),
	)
	type consumeRunners struct {
		fx.In
		Runners []platformrunner.Runner `group:"runners"`
	}
	app := fx.New(
		fx.NopLogger,
		orchAssembly,
		fx.Supply(slog.New(slog.NewTextHandler(io.Discard, nil))),
		fx.Supply(event.NewDispatcher()),
		fx.Provide(
			newNoopSessionCleaner,
			newNoopTurnStarter,
			func(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger) orchestration.AgentLauncher {
				return orchestration.NewLocalLauncher(turnStarter, logger)
			},
			// service 强依赖 RunStore（T1.2）后，TestParentFxStartup 也需补齐 stub provider。
			// service requires RunStore (T1.2), so TestParentFxStartup must also provide a stub.
			func() taskdagstore.RunStore { return &stubRunStore{} },
		),
		fx.Invoke(func(consumeRunners) {}),
	)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Start(ctx); err != nil {
		t.Fatalf("app.Start() error = %v", err)
	}
	t.Cleanup(func() { _ = app.Stop(context.Background()) })
}

// TestFxStoresAllProvided 是干式装配断言：service 依赖的四个 Store 字段
// (DAGStore / RunStore / AgentThreads / AgentBindings) 必须能被 fx 解出。
// 任一字段未注册 provider 后，if optional 会静默给零值；if 强依赖会报 missing
// dependencies。该测试位于 TestParentFxStartup 上游：仅验证 fx 能逐字段
// resolve，不拉起 lifecycle，跳开 PG 依赖。
//
// TestFxStoresAllProvided is a defensive wiring assertion: each of the four
// store fields service consumes (DAGStore / RunStore / AgentThreads /
// AgentBindings) must be resolvable by fx. If any field's provider is
// missing, an optional field would silently zero out and a required field
// would surface as a missing-dependency error. Lives upstream of
// TestParentFxStartup: only verifies fx can resolve each binding without
// pulling in lifecycle / PG.
func TestFxStoresAllProvided(t *testing.T) {
	type consumeStores struct {
		fx.In
		DAGStore      taskdagstore.OrchestrationStore
		RunStore      taskdagstore.RunStore
		AgentThreads  orchestration.AgentThreadStore
		AgentBindings orchestration.AgentBindingStore
	}
	app := fx.New(
		fx.NopLogger,
		fx.Provide(
			func() taskdagstore.OrchestrationStore { return &stubDAGStore{} },
			func() taskdagstore.RunStore { return &stubRunStore{} },
			func() orchestration.AgentThreadStore { return &stubAgentThreadStore{} },
			func() orchestration.AgentBindingStore { return &stubAgentBindingStore{} },
		),
		fx.Invoke(func(s consumeStores) error {
			if s.DAGStore == nil {
				return errors.New("DAGStore not wired")
			}
			if s.RunStore == nil {
				return errors.New("RunStore not wired")
			}
			if s.AgentThreads == nil {
				return errors.New("AgentThreads not wired")
			}
			if s.AgentBindings == nil {
				return errors.New("AgentBindings not wired")
			}
			return nil
		}),
	)
	if err := app.Err(); err != nil {
		t.Fatalf("fx.New() error = %v", err)
	}
}
