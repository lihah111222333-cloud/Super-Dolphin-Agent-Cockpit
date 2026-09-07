package orchestration

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// prepareRecoveryTerminal 在选择恢复执行器前只协调同生命周期已提交的失败或停止真相。
func (s *service) prepareRecoveryTerminal(ctx context.Context, agentID string) error {
	if s.terminalOutcomes == nil {
		return nil
	}
	if s.terminalHeadReader == nil {
		return errors.New("recovery terminal requires durable head reader")
	}
	return s.registry.withAgentLocked(agentID, func(agent *agentRuntime) error {
		head, err := s.terminalHeadReader.LoadTerminalOutcomeCurrentHead(ctx, agent.id)
		if err != nil {
			return err
		}
		if head.State == "active" {
			if !processTerminalHeadOwnerMatches(agent, head) {
				return contract.ErrTerminalOutcomeConflict
			}
			return nil
		}
		return s.projectRecoveryTerminalLocked(ctx, agent, head)
	})
}

// projectRecoveryTerminalLocked 复用唯一projector，已ACK清理仅接受原process终态完整投影证明。
func (s *service) projectRecoveryTerminalLocked(ctx context.Context, agent *agentRuntime, head contract.DurableTerminalOutcomeHead) error {
	outcome, err := s.terminalOutcomes.GetPublicTerminalOutcome(ctx, agent.id)
	if err != nil {
		return err
	}
	if agent.sessionGeneration == 0 {
		if !processTerminalProjectionAppliedAfterCleanup(agent, head, outcome) {
			return contract.ErrTerminalOutcomeConflict
		}
		return nil
	}
	if !processTerminalHeadOwnerMatches(agent, head) {
		return contract.ErrTerminalOutcomeConflict
	}
	if !terminalFailedOrStoppedString(publicTerminalState(outcome)) {
		return nil
	}
	return s.projectTerminalOutcomeLocked(ctx, agent, outcome)
}

// stopLauncherForRecovery 仅对已无活体的managed CLI省略重复Stop；reservation仍由Execution启动入口严格验证。
func (c *recoveryController) stopLauncherForRecovery(ctx context.Context, agent *agentRuntime) error {
	if _, managedCLI := c.launcher.(*cliLauncher); managedCLI && !c.launcher.IsRunning(ctx, agent) {
		return nil
	}
	return c.launcher.Stop(ctx, agent)
}
