package app

import (
	"context"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread"
	"go.uber.org/fx"
)

// threadOrchestrationParams 收集 thread orchestration adapter 依赖。
type threadOrchestrationParams struct {
	fx.In

	Service contract.OrchestrationService `optional:"true"`
}

// threadOrchestrationAdapter 将 thread 模块请求转发到 orchestration service。
type threadOrchestrationAdapter struct {
	svc contract.OrchestrationService
}

// noopThreadOrchestrationFacade 在未接 orchestration service 时保持 thread 生命周期非阻塞。
type noopThreadOrchestrationFacade struct{}

// newThreadOrchestrationFacade 根据 orchestration service 是否存在选择真实或 no-op facade。
func newThreadOrchestrationFacade(p threadOrchestrationParams) thread.OrchestrationFacade {
	if p.Service == nil {
		return noopThreadOrchestrationFacade{}
	}
	return threadOrchestrationAdapter{svc: p.Service}
}

// LaunchAgent 通过 orchestration service 启动子 agent。
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

// StopAgent 通过 orchestration service 停止 agent。
func (a threadOrchestrationAdapter) StopAgent(ctx context.Context, agentID string) error {
	return a.svc.StopAgent(ctx, agentID)
}

// Recover 通过 orchestration service 恢复 agent。
func (a threadOrchestrationAdapter) Recover(ctx context.Context, agentID string) error {
	return a.svc.Recover(ctx, agentID)
}

// BindSessionGeneration 绑定 agent 与 session generation。
func (a threadOrchestrationAdapter) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error {
	return a.svc.BindSessionGeneration(ctx, agentID, generation)
}

// LaunchAgent 在 no-op facade 中不阻塞桌面线程生命周期。
func (noopThreadOrchestrationFacade) LaunchAgent(context.Context, thread.LaunchAgentRequest) error {
	return nil
}

// StopAgent 在 no-op facade 中直接成功。
func (noopThreadOrchestrationFacade) StopAgent(context.Context, string) error {
	return nil
}

// Recover 在 no-op facade 中直接成功。
func (noopThreadOrchestrationFacade) Recover(context.Context, string) error {
	return nil
}

// BindSessionGeneration 在 no-op facade 中直接成功。
func (noopThreadOrchestrationFacade) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}
