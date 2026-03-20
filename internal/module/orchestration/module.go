package orchestration

import (
	"context"
	"errors"
	"log/slog"

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
				if err := svc.BindActiveTurnID(context.Background(), ev.AgentID, ev.TurnID); err != nil && !errors.Is(err, errAgentNotFound) && !errors.Is(err, errTurnNotActive) {
					logger.Warn("orchestration: failed to bind active turn id", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
				}
			}, logger)
			completedCancel = platformbus.ResilientSubscribe(dispatcher, func(ev turndto.TurnCompleted) {
				err := svc.CompleteTurn(context.Background(), ev.AgentID, ev.TurnID, ev.Success, ev.Error)
				if err == nil || errors.Is(err, errAgentNotFound) || errors.Is(err, errTurnNotActive) {
					return
				}
				logger.Warn("orchestration: failed to handle turn completion", "agent_id", ev.AgentID, "turn_id", ev.TurnID, "error", err)
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
