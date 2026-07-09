package orchestration

import (
	"errors"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	"github.com/kelindar/event"
)

// dagController owns DAG stores and DAG lifecycle operations for orchestration.
type dagController struct {
	logger              *slog.Logger
	eventBus            *event.Dispatcher
	dagStore            taskdag.OrchestrationStore
	runStore            taskdag.RunStore
	scheduledStartStore taskdag.ScheduledStartStore
	dispatchStore       taskdag.DispatchNodeStore
	agentThreads        AgentThreadLookup
	svcStopper          StopAgentService
}

type dagControllerParams struct {
	Logger              *slog.Logger
	EventBus            *event.Dispatcher
	DAGStore            taskdag.OrchestrationStore
	RunStore            taskdag.RunStore
	ScheduledStartStore taskdag.ScheduledStartStore
	DispatchStore       taskdag.DispatchNodeStore
	AgentThreads        AgentThreadLookup
	SvcStopper          StopAgentService
}

func newDAGController(p dagControllerParams) *dagController {
	return &dagController{
		logger:              p.Logger,
		eventBus:            p.EventBus,
		dagStore:            p.DAGStore,
		runStore:            p.RunStore,
		scheduledStartStore: p.ScheduledStartStore,
		dispatchStore:       p.DispatchStore,
		agentThreads:        p.AgentThreads,
		svcStopper:          p.SvcStopper,
	}
}

func (s *service) dagFacade() *dagController {
	if s == nil {
		return nil
	}
	return s.dagController
}

func (c *dagController) withDAGStore(fn func(taskdag.OrchestrationStore) error) error {
	if c == nil || c.dagStore == nil {
		return errors.New("dag store is not configured")
	}
	return fn(c.dagStore)
}
