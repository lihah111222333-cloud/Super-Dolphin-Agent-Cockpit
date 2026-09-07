package orchestration

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

// TestProcessExitRecoveryBoundary 锁定真实unexpected语义、旧消费槽位及不恢复的安全边界。
func TestProcessExitRecoveryBoundary(t *testing.T) {
	t.Run("unexpected-zero-exit", testUnexpectedSuccessfulCLIExit)
	t.Run("old-submit-keeps-new-slot", testOldSubmitPreservesNewExit)
	t.Run("explicit-close", TestCLIProcessExitExplicitCloseIsIgnoredByService)
	t.Run("foreign-session", TestCLIProcessExitRejectsStaleProviderSession)
	t.Run("claude-unsupported", TestManagedClaudeCLIProcessExitNeverFallsBackToLocalCommand)
	t.Run("retry-budget", TestProcessExitAutoRecoveryStopsAtRetryLimit)
}

// testUnexpectedSuccessfulCLIExit 非预期退出由进程owner标识，退出码零不能被当成用户请求停止。
func testUnexpectedSuccessfulCLIExit(t *testing.T) {
	svc, _, agent, backend := processExitAutoRecoveryFixture(t)
	backend.running, backend.reserved = false, false
	svc.handleCLIProcessExit(contract.CLIProcessExit{AgentID: agent.id, SessionID: agent.threadID, LaunchSeq: 1, Unexpected: true})
	assertProcessExitRecovered(t, svc, agent, backend)
}

// testOldSubmitPreservesNewExit 旧提交的defer不能清掉新代际真实退出，即使逻辑agent/thread相同。
func testOldSubmitPreservesNewExit(t *testing.T) {
	svc, _, agent, backend := processExitAutoRecoveryFixture(t)
	backend.running, backend.reserved = false, false
	svc.handleCLIProcessExit(contract.CLIProcessExit{AgentID: agent.id, SessionID: agent.threadID, LaunchSeq: 1, Unexpected: true})
	assertProcessExitRecovered(t, svc, agent, backend)
	pending := &pendingCLIProcessExit{agentID: agent.id, sessionID: agent.threadID, launchSeq: 2}
	agent.pendingCLIExit = pending
	svc.drainCLIProcessExitAfterSubmit(remoteTurnSubmitRef{agentID: agent.id, threadID: agent.threadID, launchSeq: 1})
	if agent.pendingCLIExit != pending || agent.launchSeq != 2 || backend.recoverCalls != 1 {
		t.Fatal("old submit consumed or recovered new lifecycle exit")
	}
	if err := svc.registry.withAgentLocked(agent.id, func(agent *agentRuntime) error {
		agent.pendingCLIExit = nil
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !backend.IsRunning(context.Background(), agent.id) {
		t.Fatal("stale submit stopped new provider")
	}
}
