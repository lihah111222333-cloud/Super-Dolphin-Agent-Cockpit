package orchestration

import "time"

// finalizeProcessExitRuntimeLocked 收口已通过 terminal commit 的 runtime 字段。
// durable terminal 已提交时保留 session generation，直到 projector ACK 后再清理；
// 非 terminal commit 继续沿用原有立即清理语义。
func (c *lifecycleController) finalizeProcessExitRuntimeLocked(
	agent *agentRuntime,
	now time.Time,
	launchSeq uint64,
	recoverViaLauncher bool,
	terminalCommitted bool,
) {
	closeAgentProcessGuard(agent)
	agent.cmd = nil
	agent.exitedAt = &now
	agent.lastExitedSeq = launchSeq
	agent.updatedAt = now
	resetRuntimeAfterProcessExitLocked(agent, recoverViaLauncher)
	if !terminalCommitted {
		c.removeSession(agent)
	}
}
