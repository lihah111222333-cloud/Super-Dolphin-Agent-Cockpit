package orchestration

import (
	"context"
	"errors"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// turnStopPort 由service生命周期owner处理提交错误后的停止请求。
type turnStopPort interface {
	stopAgentViaLauncher(ctx context.Context, agentID, reason string) error
}

// bufferCLIExitDuringSubmissionLocked 只在尚未绑定provider确认的提交窗口保留一次真实退出。
// 同一有界槽由提交owner收束后消费，不轮询、不把临时turn ID写成provider确认。
func bufferCLIExitDuringSubmissionLocked(agent *agentRuntime, event contract.CLIProcessExit) bool {
	if agent.state != agentdto.StateTurnStarting || agent.activeTurnID == "" {
		return false
	}
	bufferCLIProcessExitLocked(agent, event)
	return true
}

// drainCLIProcessExitAfterSubmit 在全部提交锁释放后消费旧attempt对应的退出；旧defer不得清掉新代际槽位。
func (s *service) drainCLIProcessExitAfterSubmit(ref remoteTurnSubmitRef) {
	s.registry.lock()
	agent, err := s.registry.lookupAgentBySeqLocked(ref.agentID, ref.launchSeq)
	if err != nil {
		s.registry.unlock()
		return
	}
	if !cliProcessExitSessionMatches(agent, ref.threadID) || agent.state == agentdto.StateTurnStarting {
		s.registry.unlock()
		return
	}
	pending := agent.pendingCLIExit
	if pending == nil || pending.agentID != ref.agentID || pending.launchSeq != ref.launchSeq || pending.sessionID != ref.threadID {
		s.registry.unlock()
		return
	}
	event, found := takePendingCLIProcessExitLocked(agent, ref.agentID, ref.launchSeq, ref.threadID)
	s.registry.unlock()
	if found {
		s.handleProcessExit(context.Background(), event.AgentID, event.LaunchSeq, event.Err)
	}
}

// dispatchBoundCLIProcessExitLocked 释放registry锁后分派已验证退出，提交窗口则交还提交owner处理。
func (s *service) dispatchBoundCLIProcessExitLocked(agent *agentRuntime, event contract.CLIProcessExit) {
	if event.Err == nil {
		event.Err = errors.New("CLI process exited unexpectedly without a stop request")
	}
	buffered := bufferCLIExitDuringSubmissionLocked(agent, event)
	s.registry.unlock()
	if !buffered {
		s.handleProcessExit(context.Background(), event.AgentID, event.LaunchSeq, event.Err)
	}
}

// SubmitTurn 在同一service owner内先持久化早到终态、确认provider保留结果，再处理提交窗口退出。
func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error {
	if s == nil || s.turns == nil {
		return errors.New("turn controller is not configured")
	}
	terminals, ref, err := s.turns.SubmitTurn(ctx, req)
	return errors.Join(err, s.settleRemoteTurnSubmission(ctx, ref, terminals))
}

// settleRemoteTurnSubmission 不允许process head抢占尚未持久化的早到模型结果。
func (s *service) settleRemoteTurnSubmission(ctx context.Context, ref remoteTurnSubmitRef, terminals []turndto.TurnTerminalV2) error {
	launcher, managedCLI := s.turns.launcher.(*cliLauncher)
	for _, terminal := range terminals {
		if err := s.handleRemoteTurnTerminalResultWithMode(ctx, terminal, managedCLI); err != nil {
			return err
		}
		if managedCLI {
			if err := launcher.reconcileTerminalReceipt(ctx, ref, terminal); err != nil {
				return err
			}
		}
	}
	s.drainCLIProcessExitAfterSubmit(ref)
	return nil
}

// reconcileTerminalReceipt 只重交付固定旧session的canonical结果，不提交provider输入或清除未确认槽位。
func (l *cliLauncher) reconcileTerminalReceipt(ctx context.Context, ref remoteTurnSubmitRef, terminal turndto.TurnTerminalV2) error {
	backend, ok := l.backend.(interface {
		ReconcileTerminalReceipt(context.Context, string, string, uint64, turndto.TurnTerminalV2) error
	})
	if !ok {
		return errors.New("CLI execution backend does not support terminal receipt reconciliation")
	}
	return backend.ReconcileTerminalReceipt(ctx, ref.agentID, ref.threadID, ref.launchSeq, terminal)
}
