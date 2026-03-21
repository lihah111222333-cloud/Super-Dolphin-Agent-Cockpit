package orchestration

import (
	"context"
	"errors"
	"log/slog"
	"time"

	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	"github.com/kelindar/event"
	"go.uber.org/fx"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	platformbus "github.com/anthropic-ai/super-agent-v3/internal/platform/bus"
)

var Module = fx.Module("orchestration",
	fx.Provide(
		NewService,
		func(s *service) Service { return s },
		NewOrchestrationHandlers,
		fx.Annotate(NewRunnerActor, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerTurnLifecycle),
	fx.Invoke(registerApprovalLifecycle),
)

func registerTurnLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	startedCancel := func() {}
	completedCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			startedCancel = platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnStarted) {
				ctx := withEventTime(context.Background(), ev.Timestamp)
				if err := svc.BindActiveTurnID(ctx, ev.AgentID, ev.TurnID); err != nil && !errors.Is(err, errAgentNotFound) && !errors.Is(err, errTurnNotActive) {
					logger.Warn("orchestration: failed to bind active turn id", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
				}
			}, logger)
			completedCancel = platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				handleTurnCompletedEvent(svc, logger, ev)
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			startedCancel()
			completedCancel()
			return nil
		},
	})
}

func registerApprovalLifecycle(lc fx.Lifecycle, dispatcher *event.Dispatcher, svc *service, logger *slog.Logger) {
	requestedCancel := func() {}
	resolvedCancel := func() {}
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			requestedCancel = platformbus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalRequested) {
				handleToolApprovalRequestedEvent(svc, loggerOrDefault(logger), ev)
			}, logger)
			resolvedCancel = platformbus.ResilientSubscribe(dispatcher, func(ev tooldto.ToolApprovalResolved) {
				handleToolApprovalResolvedEvent(svc, loggerOrDefault(logger), ev)
			}, logger)
			return nil
		},
		OnStop: func(context.Context) error {
			requestedCancel()
			resolvedCancel()
			return nil
		},
	})
}

func loggerOrDefault(logger *slog.Logger) *slog.Logger {
	if logger != nil {
		return logger
	}
	return slog.Default()
}

func withEventTime(ctx context.Context, timestamp time.Time) context.Context {
	return sharedto.WithEventTime(ctx, timestamp)
}

func resolveEventTime(ctx context.Context, fallbacks ...time.Time) time.Time {
	return sharedto.ResolveEventTime(ctx, nil, fallbacks...)
}
