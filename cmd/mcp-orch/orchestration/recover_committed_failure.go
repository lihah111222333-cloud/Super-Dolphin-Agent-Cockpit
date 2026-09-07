package orchestration

import (
	"context"
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// failRecoveryResultLocked 区分未持久化失败与新三表已提交失败，后者必须保留新身份。
func (c *recoveryController) failRecoveryResultLocked(ctx context.Context, agent *agentRuntime, attempt launcherRecoveryAttempt, cause error) error {
	if attempt.recoveryHead == nil {
		return c.failPreparedLauncherRecoveryLocked(ctx, agent, &attempt.launching, cause)
	}
	terminalErr := c.launchCommit.failCommittedRecoveryLocked(ctx, agent, *attempt.recoveryHead, cause)
	c.registry.unlock()
	stopErr := c.launcher.Stop(ctx, &attempt.launching)
	return errors.Join(cause, terminalErr, stopErr)
}

// failCommittedRecoveryLocked 在新head上写失败真相并复用唯一projector；失败写不可用也不清除新围栏。
func (s *service) failCommittedRecoveryLocked(ctx context.Context, agent *agentRuntime, head contract.TerminalOutcomeHead, cause error) error {
	if agent.sessionGeneration != head.Generation || agent.terminalHeadVersion != head.Version || agent.remoteThreadID != head.PublicThreadID {
		return errors.Join(cause, contract.ErrTerminalOutcomeConflict)
	}
	commit, err := terminalOutcomeCommitFromAgentTerminal(agent, head.PublicThreadID, head.SessionID, resolveEventTime(ctx, time.Now().UTC()), processTerminalProjectionKind(agent))
	if err != nil {
		return err
	}
	commit.Identity.ExpectedActiveState = head.ExpectedActiveState
	if _, err := s.terminalOutcomes.CommitTerminalOutcome(ctx, commit); err != nil {
		return err
	}
	return s.projectTerminalOutcomeLocked(ctx, agent, commit)
}

// rejectRecoveryResultLocked 对已提交CLI保留新真相；其它launcher保持原stale结果清理语义。
func (c *recoveryController) rejectRecoveryResultLocked(ctx context.Context, agent *agentRuntime, attempt launcherRecoveryAttempt, cause error) error {
	if attempt.recoveryHead != nil {
		return c.failRecoveryResultLocked(ctx, agent, attempt, cause)
	}
	c.registry.unlock()
	return c.discardStaleSuccessfulLaunch(ctx, &attempt.launching, cause)
}

// rollbackRecoveryResultLocked 只为未持久化launcher保留旧runtime清理规则。
func (c *recoveryController) rollbackRecoveryResultLocked(ctx context.Context, agent *agentRuntime, attempt launcherRecoveryAttempt, cause error) error {
	if attempt.recoveryHead != nil {
		return c.failRecoveryResultLocked(ctx, agent, attempt, cause)
	}
	return c.rollbackRecoveryCommitSuccessFailure(ctx, agent, &attempt.launching, cause)
}

// finishRecoveryResultErrorLocked 已持久CLI的重放失败必须关闭新进程，非CLI保持原错误传播。
func (c *recoveryController) finishRecoveryResultErrorLocked(ctx context.Context, agent *agentRuntime, attempt launcherRecoveryAttempt, cause error) error {
	if attempt.recoveryHead != nil {
		return c.failRecoveryResultLocked(ctx, agent, attempt, cause)
	}
	c.registry.unlock()
	return cause
}
