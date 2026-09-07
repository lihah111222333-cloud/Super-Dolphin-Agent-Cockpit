package orchestration

import (
	"context"
	"errors"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
)

// recoverLaunch 仅通过内部恢复端口恢复同一provider线程，不把普通Launch参数当恢复指令。
func (l *cliLauncher) recoverLaunch(ctx context.Context, agent *agentRuntime, req LaunchRequest) (LaunchResult, error) {
	backend, ok := l.backend.(interface {
		Recover(context.Context, PersistedRuntimeActivation) (LaunchResult, error)
	})
	if !ok {
		return LaunchResult{}, errors.New("CLI execution backend does not support native recovery")
	}
	provider, _ := snapshotProvider(agent)
	activation := PersistedRuntimeActivation{AgentID: agent.id, ThreadID: agent.remoteThreadID, Provider: provider, CWD: req.Cwd}
	result, err := backend.Recover(contract.WithLaunchSeq(ctx, agent.launchSeq), activation)
	if err != nil {
		return LaunchResult{}, err
	}
	if result.ThreadID != activation.ThreadID || result.RemoteAgentID != activation.AgentID {
		return LaunchResult{}, errors.New("CLI recovery returned a foreign identity")
	}
	return result, nil
}

// recoverManagedCLI 采用provider真实同线程恢复，不插入新的binding或thread。
func (c *recoveryController) recoverManagedCLI(ctx context.Context, launcher *cliLauncher, attempt launcherRecoveryAttempt) error {
	result, err := launcher.recoverLaunch(ctx, &attempt.launching, attempt.req)
	if err != nil {
		return c.commitLauncherRecoveryFailure(ctx, attempt, err)
	}
	return c.commitLauncherRecoverySuccess(ctx, attempt, result)
}

// bindRecoveryHeadLocked 在发布idle前共用terminal owner CAS，并保留已提交head供失败收口。
func (c *recoveryController) bindRecoveryHeadLocked(ctx context.Context, agent *agentRuntime, attempt *launcherRecoveryAttempt) error {
	if _, managedCLI := c.launcher.(*cliLauncher); !managedCLI {
		return nil
	}
	head, err := c.launchCommit.bindDurableSessionGenerationLocked(ctx, agent, agent.launchSeq, agentdto.StateIdle)
	if err != nil {
		return err
	}
	attempt.recoveryHead = &head
	return nil
}

var errStaleProcessExitRecovery = errors.New("process-exit recovery lifecycle fence mismatch")

// validateManagedCLIRecoveryLocked 在状态变更前拒绝未终结输入与过期退出，不能以新会话重放未知操作。
func (c *recoveryController) validateManagedCLIRecoveryLocked(ctx context.Context, agent *agentRuntime, reason string) error {
	if !isManagedCLIRecovery(c.launcher, agent) {
		return nil
	}
	if agent.activeTurnID != "" {
		return errors.New("CLI recovery requires the previous turn to be durably terminal; input replay is forbidden")
	}
	if reason != recoverReasonProcessExit {
		return nil
	}
	seq, ok := contract.LaunchSeqFromContext(ctx)
	if !ok || seq == 0 || agent.launchSeq != seq || agent.lastExitedSeq != seq || agent.stopRequested {
		return errStaleProcessExitRecovery
	}
	return nil
}
