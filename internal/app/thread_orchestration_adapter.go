package app

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/module/orchestration"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
)

type threadOrchestrationAdapter struct {
	svc orchestration.Service
}

func newThreadOrchestrationFacade(svc orchestration.Service) thread.OrchestrationFacade {
	return threadOrchestrationAdapter{svc: svc}
}

func (a threadOrchestrationAdapter) LaunchAgent(ctx context.Context, req thread.LaunchAgentRequest) error {
	return a.svc.LaunchAgent(ctx, orchestration.LaunchRequest{
		AgentID:  req.AgentID,
		Name:     req.Name,
		ParentID: req.ParentID,
		Cwd:      req.Cwd,
		Command:  req.Command,
		Env:      req.Env,
	})
}

func (a threadOrchestrationAdapter) StopAgent(ctx context.Context, agentID string) error {
	return a.svc.StopAgent(ctx, agentID)
}

func (a threadOrchestrationAdapter) Recover(ctx context.Context, agentID string) error {
	return a.svc.Recover(ctx, agentID)
}
