package app

import (
	"context"
	"errors"

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

// errOrchestrationServiceUnavailable 表示 thread facade 缺少真实 orchestration service。
var errOrchestrationServiceUnavailable = errors.New("orchestration service is unavailable")

// missingThreadOrchestrationFacade 在未接 orchestration service 时阻断生命周期动作。
type missingThreadOrchestrationFacade struct{}

// newThreadOrchestrationFacade 根据 orchestration service 是否存在选择真实或 fail-fast facade。
func newThreadOrchestrationFacade(p threadOrchestrationParams) thread.OrchestrationFacade {
	if p.Service == nil {
		return missingThreadOrchestrationFacade{}
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

// LaunchAgent 在缺少 orchestration service 时返回显式错误，避免制造启动成功假象。
func (missingThreadOrchestrationFacade) LaunchAgent(context.Context, thread.LaunchAgentRequest) error {
	return errOrchestrationServiceUnavailable
}

// StopAgent 在缺少 orchestration service 时返回显式错误，避免调用方误判已停止。
func (missingThreadOrchestrationFacade) StopAgent(context.Context, string) error {
	return errOrchestrationServiceUnavailable
}

// Recover 在缺少 orchestration service 时返回显式错误，避免恢复流程被静默跳过。
func (missingThreadOrchestrationFacade) Recover(context.Context, string) error {
	return errOrchestrationServiceUnavailable
}

// BindSessionGeneration 在缺少 orchestration service 时返回显式错误，避免 session 绑定成功假象。
func (missingThreadOrchestrationFacade) BindSessionGeneration(context.Context, string, uint64) error {
	return errOrchestrationServiceUnavailable
}
