package orchestration

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// CLIExecutionBackend 是独立 CLI 执行器端口；不包含宿主 RPC 或私有 agentRuntime。
type CLIExecutionBackend interface {
	Launch(context.Context, LaunchRequest) (LaunchResult, error)
	Fork(context.Context, string, LaunchRequest) (LaunchResult, error)
	Stop(context.Context, string) error
	Archive(context.Context, string) error
	Interrupt(context.Context, string, string) error
	SubmitTurn(context.Context, TurnSubmission) (string, error)
	IsRunning(context.Context, string) bool
	BindTerminalSink(func(context.Context, turndto.TurnTerminalV2))
}

// PersistedRuntimeActivation 是 daemon 重启后重新绑定 provider thread 的最小身份。
// 它不携带 prompt、凭据或调用方可伪造的 parent thread 字段。
type PersistedRuntimeActivation struct {
	AgentID, ThreadID, Provider, CWD string
}

type cliLauncher struct{ backend CLIExecutionBackend }

// bindRemoteEventSink 复用既有提交 fence/早到终态收敛路径，不新增状态 writer。
func (l *cliLauncher) bindRemoteEventSink(sink remoteLauncherEventSink) {
	// backend 暴露可确认接缝时优先保留该能力；若降级到旧 void sink，durable
	// commit/projection 失败仍可能被 provider 误认为已 ACK。
	boundReceipt := false
	if receiptBinder, ok := l.backend.(interface {
		BindTerminalSinkWithReceipt(func(context.Context, turndto.TurnTerminalV2) error)
	}); ok {
		if receiptSink, ok := sink.(interface {
			handleRemoteTurnTerminalResult(context.Context, turndto.TurnTerminalV2) error
		}); ok {
			receiptBinder.BindTerminalSinkWithReceipt(receiptSink.handleRemoteTurnTerminalResult)
			boundReceipt = true
		}
	}
	if !boundReceipt {
		l.backend.BindTerminalSink(sink.handleRemoteTurnTerminal)
	}
	if exitBinder, ok := l.backend.(interface {
		BindProcessExitSink(func(contract.CLIProcessExit))
	}); ok {
		exitBinder.BindProcessExitSink(func(event contract.CLIProcessExit) {
			if owner, ok := sink.(interface{ handleCLIProcessExit(contract.CLIProcessExit) }); ok {
				owner.handleCLIProcessExit(event)
			}
		})
	}
}

// NewCLILauncher 将独立执行器接入现有生命周期 owner；nil 不会退回本地自执行。
func NewCLILauncher(backend CLIExecutionBackend) (AgentLauncher, error) {
	if backend == nil {
		return nil, errors.New("CLI execution backend is required")
	}
	return &cliLauncher{backend: backend}, nil
}

// ActivatePersistedRuntime 要求 backend 证明已按持久 thread 恢复真实 provider session。
func (l *cliLauncher) ActivatePersistedRuntime(ctx context.Context, activation PersistedRuntimeActivation) error {
	if l == nil || l.backend == nil {
		return errors.New("CLI execution backend is required")
	}
	activator, ok := l.backend.(interface {
		ActivatePersistedRuntime(context.Context, PersistedRuntimeActivation) error
	})
	if !ok {
		return errors.New("CLI execution backend does not support persisted runtime activation")
	}
	return activator.ActivatePersistedRuntime(ctx, activation)
}

// SupportsPersistedRuntimeRehydrate 仅在 backend 提供真实 session activation 时声明支持。
func (l *cliLauncher) SupportsPersistedRuntimeRehydrate() bool {
	if l == nil || l.backend == nil {
		return false
	}
	_, ok := l.backend.(interface {
		ActivatePersistedRuntime(context.Context, PersistedRuntimeActivation) error
	})
	return ok
}

// Launch 将同一编排请求交给 CLI 执行器，不启动 sidecar 自身。
func (l *cliLauncher) Launch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	if agent != nil {
		ctx = contract.WithLaunchSeq(ctx, agent.launchSeq)
	}
	return l.backend.Launch(ctx, req)
}

// Fork 只以已有 parent 身份执行显式分叉。
func (l *cliLauncher) Fork(ctx context.Context, parent, child *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	if parent == nil {
		return LaunchResult{}, contract.ErrAgentNotFound
	}
	if child != nil {
		ctx = contract.WithLaunchSeq(ctx, child.launchSeq)
	}
	return l.backend.Fork(ctx, parent.id, req)
}

// Stop 停止执行器拥有的目标 agent。
func (l *cliLauncher) Stop(ctx context.Context, agent *agentRuntime) error {
	return l.backend.Stop(ctx, agent.id)
}

// Archive 由执行器关闭目标 session 并保留持久身份。
func (l *cliLauncher) Archive(ctx context.Context, agent *agentRuntime) error {
	return l.backend.Archive(ctx, agent.id)
}

// Interrupt 只打断当前 turn，不与停止整个 agent 混同。
func (l *cliLauncher) Interrupt(ctx context.Context, agent *agentRuntime, source string) error {
	return l.backend.Interrupt(ctx, agent.id, source)
}

// SubmitTurn 保留规范 submission 交给独立执行器。
func (l *cliLauncher) SubmitTurn(ctx context.Context, _ *agentRuntime, submission TurnSubmission) (string, error) {
	return l.backend.SubmitTurn(ctx, submission)
}

// IsRunning 查询执行器活体状态，不把逻辑快照当存活证据。
func (l *cliLauncher) IsRunning(ctx context.Context, agent *agentRuntime) bool {
	return agent != nil && l.backend.IsRunning(ctx, agent.id)
}
