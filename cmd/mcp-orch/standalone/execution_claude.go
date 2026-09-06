package standalone

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// launchClaude 预分配官方 CLI 支持的 session UUID，首条输入后还需确认 system/init。
func (e *Execution) launchClaude(ctx context.Context, session *cliSession, req contract.LaunchRequest, provider Provider, model string) (orchestration.LaunchResult, error) {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return orchestration.LaunchResult{}, err
	}
	identity, err := uuid.NewRandom()
	if err != nil {
		return orchestration.LaunchResult{}, err
	}
	id := identity.String()
	session.mu.Lock()
	session.threadID = id
	session.claudeAttachPending = true
	session.mu.Unlock()
	process, err := claudewire.StartWithExitContext(ctx, provider.Executable, req.Cwd, model, id, req.Instructions, cliEnvironment(provider), e.claudeEventHandler(session), contract.CLIProcessExitBinding{
		AgentID: req.AgentID, SessionID: id, LaunchSeq: session.launchSeq, OnExit: e.processExitHandler(session),
	})
	if err != nil {
		session.mu.Lock()
		session.claudeAttachPending = false
		session.mu.Unlock()
		e.failSession(session, err)
		return orchestration.LaunchResult{}, err
	}
	session.mu.Lock()
	if session.stopped || session.failed != nil {
		failure := session.failed
		if failure == nil {
			failure = errors.New("Claude launch cancelled")
		}
		session.claudeAttachPending = false
		session.mu.Unlock()
		return orchestration.LaunchResult{}, errors.Join(failure, process.Close())
	}
	if err := ctx.Err(); err != nil {
		session.claudeAttachPending = false
		session.mu.Unlock()
		cancelErr := errors.Join(errors.New("Claude launch cancelled"), process.Close(), err)
		e.failSession(session, cancelErr)
		return orchestration.LaunchResult{}, cancelErr
	}
	session.claude = process
	session.claudeAttachPending = false
	session.activated = true
	initialized := session.claudeInitialized
	session.mu.Unlock()
	// The reader may have observed system/init before StartWithExitContext
	// returned. Only signal readiness after the process handle is attached.
	if initialized {
		session.signalReady(nil)
	}
	return orchestration.LaunchResult{ThreadID: id, RemoteAgentID: req.AgentID}, nil
}

// submitClaude 使用一次明确的请求 UUID，写入失败后不自动重发。
func (e *Execution) submitClaude(ctx context.Context, session *cliSession, sub contract.TurnSubmission) (string, error) {
	parts := make([]string, 0, len(sub.Inputs))
	for _, item := range sub.Inputs {
		parts = append(parts, item.Content)
	}
	session.mu.Lock()
	// 官方 CLI 先接收首条 user 再发送 system/init；已绑定进程即可投递，
	// 但 ready 和结果发布仍由真实 init 确认，turnID 在本锁内占位以拒绝重复投递。
	if session.stopped || session.failed != nil || session.claude == nil || session.turnID != "" {
		session.mu.Unlock()
		return "", errors.New("Claude session is unavailable or busy")
	}
	identity, err := uuid.NewRandom()
	if err != nil {
		session.mu.Unlock()
		return "", err
	}
	turnID := identity.String()
	session.turnID, session.output = turnID, ""
	process, threadID := session.claude, session.threadID
	session.mu.Unlock()
	if err := process.Send(ctx, threadID, turnID, strings.Join(parts, "\n")); err != nil {
		e.failSession(session, err)
		return "", err
	}
	return turnID, nil
}

// claudeEventHandler 只将当前根 session 的 result 转成规范终态；init 竞态期间保留一条有界结果。
func (e *Execution) claudeEventHandler(session *cliSession) func(claudewire.Event) {
	return func(event claudewire.Event) {
		if pending, ok := e.acceptClaudeInit(session, event); ok {
			e.replayClaudePendingResult(session, pending)
			return
		}
		e.handleClaudeRootResult(session, event)
	}
}

// replayClaudePendingResult 在 init 确认后重放唯一缓冲的根 result。
func (e *Execution) replayClaudePendingResult(session *cliSession, pending *claudewire.Event) {
	if pending == nil {
		return
	}
	e.finalizeClaudeResult(session, *pending)
}

// handleClaudeRootResult 过滤非根事件，并在 init 竞态期间缓冲根 result。
func (e *Execution) handleClaudeRootResult(session *cliSession, event claudewire.Event) {
	if event.Type != "result" || event.ParentToolUseID != nil {
		return
	}
	session.mu.Lock()
	if event.SessionID != session.threadID {
		session.mu.Unlock()
		return
	}
	// Let finalizeClaudeResult inspect the retained receipt before applying the
	// ordinary stopped/active-turn fences. A stopped provider can still have a
	// consumer acknowledgement to recover, but it must not start a new turn.
	if session.pendingTerminal != nil {
		session.mu.Unlock()
		e.finalizeClaudeResult(session, event)
		return
	}
	if session.stopped || session.turnID == "" {
		session.mu.Unlock()
		return
	}
	if !session.claudeInitialized {
		bufferClaudeRootResult(session, event)
		failed := session.failed
		session.mu.Unlock()
		if failed != nil {
			session.signalReady(failed)
		}
		return
	}
	session.mu.Unlock()
	e.finalizeClaudeResult(session, event)
}

// bufferClaudeRootResult 在持锁状态下保留首个根 result，冲突时保持失败闭锁。
func bufferClaudeRootResult(session *cliSession, event claudewire.Event) {
	if session.pendingClaudeResult == nil {
		pending := event
		session.pendingClaudeResult = &pending
		return
	}
	if sameClaudeRootResult(*session.pendingClaudeResult, event) || session.failed != nil {
		return
	}
	session.failed = errors.New("conflicting Claude root results before system/init")
}

func (*Execution) acceptClaudeInit(session *cliSession, event claudewire.Event) (*claudewire.Event, bool) {
	if !event.IsSystemInit(session.threadID) {
		return nil, false
	}
	session.mu.Lock()
	if session.stopped {
		session.mu.Unlock()
		return nil, true
	}
	session.claudeInitialized = true
	pending := session.pendingClaudeResult
	session.pendingClaudeResult = nil
	attached := !session.claudeAttachPending
	session.mu.Unlock()
	if attached {
		session.signalReady(nil)
	}
	return pending, true
}

// finalizeClaudeResult 将已确认的根 result 与当前服务 turn 绑定，并保证只终结一次。
func (e *Execution) finalizeClaudeResult(session *cliSession, event claudewire.Event) {
	if session == nil {
		return
	}
	session.mu.Lock()
	if event.SessionID != session.threadID {
		session.mu.Unlock()
		return
	}
	// A failed strict receipt keeps the canonical terminal in one bounded slot.
	// Replaying the exact provider result retries only the consumer sink; a
	// different result for the same service turn is rejected and cannot replace
	// the first canonical truth.
	if pending := session.pendingTerminal; pending != nil {
		session.mu.Unlock()
		e.retryClaudeReceipt(session, event, pending)
		return
	}
	if session.stopped || session.turnID == "" || !session.claudeInitialized {
		session.mu.Unlock()
		return
	}
	terminal, truth, err := buildClaudeTerminalForSession(session, event)
	if err != nil {
		session.mu.Unlock()
		e.failSession(session, err)
		return
	}
	pending, _, claimErr := claimTerminalReceiptLocked(session, truth, terminal)
	if claimErr != nil {
		session.mu.Unlock()
		e.failSession(session, claimErr)
		return
	}
	session.mu.Unlock()
	_ = e.finishTerminalReceipt(session, pending)
}

func (e *Execution) retryClaudeReceipt(session *cliSession, event claudewire.Event, pending *pendingTerminalReceipt) {
	truth := claudeTerminalTruth(event, pending.terminal.TurnID)
	if pending.truth != truth {
		e.failSession(session, fmt.Errorf("Claude terminal truth conflict for turn %q", pending.terminal.TurnID))
		return
	}
	session.mu.Lock()
	if pending.inFlight {
		session.mu.Unlock()
		return
	}
	pending.inFlight, pending.retryable = true, false
	session.mu.Unlock()
	_ = e.finishTerminalReceipt(session, pending)
}

func buildClaudeTerminalForSession(session *cliSession, event claudewire.Event) (turndto.TurnTerminalV2, cliTerminalTruth, error) {
	turnID := session.turnID
	failed := session.failed != nil
	text := ""
	if !failed {
		result, err := event.ResultText(event.SessionID)
		if err != nil {
			failed = true
		} else {
			text = result
		}
	}
	return buildClaudeTerminal(event, turnID, text, failed)
}

// sameClaudeRootResult 比较根 result 的完整身份与内容，避免重复终结或吞掉冲突。
func sameClaudeRootResult(left, right claudewire.Event) bool {
	if left.Type != right.Type || left.Subtype != right.Subtype || left.SessionID != right.SessionID ||
		left.UUID != right.UUID || left.IsError != right.IsError || left.Result != right.Result {
		return false
	}
	if left.ParentToolUseID == nil || right.ParentToolUseID == nil {
		return left.ParentToolUseID == nil && right.ParentToolUseID == nil
	}
	return *left.ParentToolUseID == *right.ParentToolUseID
}
