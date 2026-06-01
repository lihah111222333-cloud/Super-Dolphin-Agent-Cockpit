package app

import (
	"context"
	"errors"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"go.uber.org/fx"
)

type threadOrchestrationParams struct {
	fx.In

	Service contract.OrchestrationService `optional:"true"`
}

type threadOrchestrationAdapter struct {
	svc contract.OrchestrationService
}

type noopThreadOrchestrationFacade struct{}

func newThreadOrchestrationFacade(p threadOrchestrationParams) thread.OrchestrationFacade {
	if p.Service == nil {
		return noopThreadOrchestrationFacade{}
	}
	return threadOrchestrationAdapter{svc: p.Service}
}

func (a threadOrchestrationAdapter) LaunchAgent(ctx context.Context, req thread.LaunchAgentRequest) error {
	return a.svc.LaunchAgent(ctx, contract.LaunchRequest{
		AgentID:     req.AgentID,
		Name:        req.Name,
		ParentID:    req.ParentID,
		AgentType:   req.AgentType,
		MemoryScope: req.MemoryScope,
		Cwd:         req.Cwd,
		Command:     req.Command,
		Env:         req.Env,
	})
}

func (a threadOrchestrationAdapter) StopAgent(ctx context.Context, agentID string) error {
	return a.svc.StopAgent(ctx, agentID)
}

func (a threadOrchestrationAdapter) Recover(ctx context.Context, agentID string) error {
	return a.svc.Recover(ctx, agentID)
}

func (a threadOrchestrationAdapter) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	return a.svc.BindSessionGeneration(ctx, agentID, generation)
}

func (noopThreadOrchestrationFacade) LaunchAgent(context.Context, thread.LaunchAgentRequest) error {
	return errors.New("orchestration: service not configured")
}

func (noopThreadOrchestrationFacade) StopAgent(context.Context, string) error {
	return errors.New("orchestration: service not configured")
}

func (noopThreadOrchestrationFacade) Recover(context.Context, string) error {
	return errors.New("orchestration: service not configured")
}

func (noopThreadOrchestrationFacade) BindSessionGeneration(context.Context, string, uint64) error {
	return errors.New("orchestration: service not configured")
}
