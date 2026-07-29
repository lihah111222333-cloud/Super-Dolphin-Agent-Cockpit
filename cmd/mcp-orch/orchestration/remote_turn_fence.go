package orchestration

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/launcherrors"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// trySubmitRemoteTurn 以本地代际快照提交远端 turn，并收束提交窗口内提前到达的终态。
func (c *turnController) trySubmitRemoteTurn(ctx context.Context, agentID string, req TurnSubmission) (bool, []turndto.TurnTerminalV2, error) {
	attempt, handled, err := c.prepareRemoteTurnSubmit(ctx, agentID, req)
	if !handled || err != nil {
		return handled, nil, err
	}
	if err := c.beginRemoteTurnSubmit(attempt); err != nil {
		c.finishRemoteTurnSubmitFailure(ctx, attempt, err)
		return true, nil, err
	}
	remoteTurnID, submitErr := c.launcher.SubmitTurn(ctx, &attempt.agent, attempt.req)
	if submitErr != nil {
		return true, nil, c.handleRemoteTurnSubmitError(ctx, attempt, submitErr)
	}
	terminals, finishErr := c.finishRemoteTurnSubmitSuccess(ctx, attempt, remoteTurnID)
	if finishErr == nil {
		return true, terminals, nil
	}
	cleanupErr := c.interruptRemoteTurnSubmitOrphan(attempt, remoteTurnID)
	c.finishRemoteTurnSubmitFailure(ctx, attempt, finishErr)
	return true, nil, errors.Join(finishErr, cleanupErr)
}

func (c *turnController) handleRemoteTurnSubmitError(ctx context.Context, attempt remoteTurnSubmitAttempt, submitErr error) error {
	c.finishRemoteTurnSubmitFailure(ctx, attempt, submitErr)
	if launcherrors.Classify(submitErr) != launcherrors.ClassPermanent {
		return submitErr
	}
	cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
	defer cancel()
	if c.stopper == nil {
		return errors.Join(submitErr, errors.New("turn stop port is not configured"))
	}
	return errors.Join(submitErr, c.stopper.stopAgentViaLauncher(cleanupCtx, attempt.agentID, "remote_turn_submit_failed"))
}

// prepareRemoteTurnSubmit 在 agent 锁内冻结远端提交身份并推进本地状态机。
func (c *turnController) prepareRemoteTurnSubmit(ctx context.Context, agentID string, req TurnSubmission) (remoteTurnSubmitAttempt, bool, error) {
	attempt := remoteTurnSubmitAttempt{}
	handled := true
	err := c.withAgentLocked(agentID, func(agent *agentRuntime) error {
		if !c.canSubmitViaLauncher(ctx, agent) {
			handled = false
			return nil
		}
		if agent.stopRequested {
			return fmt.Errorf("%w: agent %q is stopping", errAgentStoppingForStopper, agent.id)
		}
		if remoteAgentBusy(agent) {
			return fmt.Errorf("agent %q is busy", agent.id)
		}
		req.AgentID = agentID
		req.ExpectedTurnID = c.turnIDFor(req)
		if threadID := strings.TrimSpace(req.ThreadID); threadID != "" {
			agent.threadID = threadID
		}
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnEnqueued); err != nil {
			return err
		}
		agent.activeTurnID = req.ExpectedTurnID
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			agent.activeTurnID = ""
			return err
		}
		agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
		attempt = remoteTurnSubmitAttempt{
			agentID: agentID, turnID: req.ExpectedTurnID,
			threadID: strings.TrimSpace(agent.remoteThreadID), launchSeq: agent.launchSeq,
			req: req, agent: *agent,
		}
		return nil
	})
	return attempt, handled, err
}

func (c *turnController) finishRemoteTurnSubmitSuccess(ctx context.Context, attempt remoteTurnSubmitAttempt, remoteTurnID string) ([]turndto.TurnTerminalV2, error) {
	if c == nil || c.registry == nil {
		return nil, errors.New("turn controller is not configured")
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	pending := c.pendingRemoteTurnSubmits[attempt.ref()]
	if pending.err != nil {
		return nil, pending.err
	}
	c.registry.lock()
	defer c.registry.unlock()
	terminals, err := c.bindRemoteTurnSubmitLocked(ctx, attempt, remoteTurnID, pending.terminals)
	if err != nil {
		return nil, err
	}
	c.takePendingRemoteTerminalsLocked(attempt.ref())
	return terminals, nil
}

func (c *turnController) bindRemoteTurnSubmitLocked(ctx context.Context, attempt remoteTurnSubmitAttempt, remoteTurnID string, pending []turndto.TurnTerminalV2) ([]turndto.TurnTerminalV2, error) {
	agent, err := c.registry.lookupAgentByIDLocked(attempt.agentID)
	if err != nil {
		return nil, fmt.Errorf("remote turn submit ownership changed for agent %q: %w", attempt.agentID, err)
	}
	if !remoteTurnSubmitStillOwned(agent, attempt) {
		return nil, fmt.Errorf("remote turn submit drift for agent %q turn %q", attempt.agentID, attempt.turnID)
	}
	canonicalTurnID := shared.FirstTrimmed(remoteTurnID, attempt.turnID)
	if err := c.bindRemoteTurnIDLocked(ctx, agent, canonicalTurnID); err != nil {
		return nil, err
	}
	return matchingRemoteTerminals(pending, canonicalTurnID), nil
}

func remoteTurnSubmitStillOwned(agent *agentRuntime, attempt remoteTurnSubmitAttempt) bool {
	return strings.TrimSpace(agent.remoteThreadID) == attempt.threadID &&
		agent.launchSeq == attempt.launchSeq &&
		strings.TrimSpace(agent.activeTurnID) == attempt.turnID
}

func (c *turnController) bindRemoteTurnIDLocked(ctx context.Context, agent *agentRuntime, turnID string) error {
	expectedState := string(agent.state)
	if agent.state == agentdto.StateTurnStarting {
		expectedState = string(agentdto.StateTurnRunning)
	}
	head, err := c.activateTerminalTurnHeadLocked(ctx, agent, turnID, expectedState)
	if err != nil {
		return err
	}
	agent.activeTurnID = turnID
	if agent.state == agentdto.StateTurnStarting {
		if err := c.fireOrForceLocked(ctx, agent, agentdto.TriggerTurnAccepted); err != nil {
			return fmt.Errorf("mark remote turn %q running: %w", turnID, err)
		}
	}
	agent.updatedAt = resolveEventTime(ctx, agent.updatedAt, agent.startedAt)
	agent.terminalHeadVersion = head.Version
	return nil
}

func matchingRemoteTerminals(terminals []turndto.TurnTerminalV2, turnID string) []turndto.TurnTerminalV2 {
	matched := make([]turndto.TurnTerminalV2, 0, len(terminals))
	for _, terminal := range terminals {
		if strings.TrimSpace(terminal.TurnID) == turnID {
			matched = append(matched, terminal)
		}
	}
	return matched
}

func (c *turnController) interruptRemoteTurnSubmitOrphan(attempt remoteTurnSubmitAttempt, remoteTurnID string) error {
	if c == nil || c.launcher == nil {
		return errors.New("remote turn orphan cleanup launcher is not configured")
	}
	orphan := attempt.agent
	orphan.activeTurnID = shared.FirstTrimmed(remoteTurnID, attempt.turnID)
	cleanupCtx, cancel := platformconfig.WithTimeout(context.Background(), platformconfig.AsyncLaunchTimeout)
	defer cancel()
	if err := c.launcher.Interrupt(cleanupCtx, &orphan, "remote_turn_submit_drift"); err != nil {
		return fmt.Errorf("interrupt remote turn orphan thread %q turn %q: %w", attempt.threadID, orphan.activeTurnID, err)
	}
	return nil
}

func (c *turnController) finishRemoteTurnSubmitFailure(ctx context.Context, attempt remoteTurnSubmitAttempt, submitErr error) {
	c.cancelRemoteTurnSubmit(attempt)
	c.finishTurnStartFailure(ctx, turnWork{agentID: attempt.agentID, turnID: attempt.turnID}, submitErr)
}

type remoteTurnSubmitRef struct {
	agentID           string
	threadID          string
	launchSeq         uint64
	provisionalTurnID string
}

func (a remoteTurnSubmitAttempt) ref() remoteTurnSubmitRef {
	return remoteTurnSubmitRef{
		agentID: a.agentID, threadID: a.threadID,
		launchSeq: a.launchSeq, provisionalTurnID: a.turnID,
	}
}

type pendingRemoteTurnSubmit struct {
	terminals []turndto.TurnTerminalV2
	seen      map[remoteTerminalTruth]struct{}
	err       error
}

const defaultPendingRemoteTerminalCapacity = 4096

func (c *turnController) beginRemoteTurnSubmit(attempt remoteTurnSubmitAttempt) error {
	if c == nil {
		return errors.New("turn controller is required")
	}
	ref := attempt.ref()
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	if c.pendingRemoteTurnSubmits == nil {
		c.pendingRemoteTurnSubmits = make(map[remoteTurnSubmitRef]pendingRemoteTurnSubmit)
	}
	if _, exists := c.pendingRemoteTurnSubmits[ref]; exists {
		return fmt.Errorf("remote turn submit reconciliation already exists for agent %q", attempt.agentID)
	}
	c.pendingRemoteTurnSubmits[ref] = pendingRemoteTurnSubmit{seen: make(map[remoteTerminalTruth]struct{})}
	return nil
}

func (c *turnController) cancelRemoteTurnSubmit(attempt remoteTurnSubmitAttempt) {
	if c == nil {
		return
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	c.takePendingRemoteTerminalsLocked(attempt.ref())
}

func (c *turnController) takePendingRemoteTerminalsLocked(ref remoteTurnSubmitRef) pendingRemoteTurnSubmit {
	pending := c.pendingRemoteTurnSubmits[ref]
	delete(c.pendingRemoteTurnSubmits, ref)
	c.pendingRemoteTerminalCount -= len(pending.terminals)
	return pending
}

func (c *turnController) pendingRemoteTerminalLimitLocked() int {
	if c.pendingRemoteTerminalCapacity > 0 {
		return c.pendingRemoteTerminalCapacity
	}
	return defaultPendingRemoteTerminalCapacity
}

// routeRemoteTurnTerminal 只接收当前代际的远端终态，并在提交窗口内执行有界缓冲。
func (c *turnController) routeRemoteTurnTerminal(agentID string, terminal turndto.TurnTerminalV2) (deliver, buffered bool, err error) {
	if c == nil || c.registry == nil {
		return false, false, errors.New("turn controller is not configured")
	}
	c.remoteTerminalMu.Lock()
	defer c.remoteTerminalMu.Unlock()
	c.registry.lock()
	ref, found := c.registry.remoteTerminalTargetTurnRefLocked(agentID)
	c.registry.unlock()
	if !found || ref.provisionalTurnID == "" || ref.threadID != strings.TrimSpace(terminal.ThreadID) {
		return false, false, nil
	}
	return c.routeRemoteTurnTerminalLocked(ref, terminal)
}

func (c *turnController) routeRemoteTurnTerminalLocked(ref remoteTurnSubmitRef, terminal turndto.TurnTerminalV2) (deliver, buffered bool, err error) {
	pending, exists := c.pendingRemoteTurnSubmits[ref]
	if exists {
		return c.bufferEarlyRemoteTerminalLocked(ref, pending, terminal)
	}
	return ref.provisionalTurnID == strings.TrimSpace(terminal.TurnID), false, nil
}

func (c *turnController) bufferEarlyRemoteTerminalLocked(ref remoteTurnSubmitRef, pending pendingRemoteTurnSubmit, terminal turndto.TurnTerminalV2) (bool, bool, error) {
	if pending.err != nil {
		return false, false, pending.err
	}
	truth, err := remoteTerminalTruthFor(terminal)
	if err != nil {
		return c.failPendingRemoteTerminalLocked(ref, pending, fmt.Errorf("fingerprint early remote terminal: %w", err))
	}
	if _, duplicate := pending.seen[truth]; duplicate {
		return false, true, nil
	}
	if c.pendingRemoteTerminalCount >= c.pendingRemoteTerminalLimitLocked() {
		return c.failPendingRemoteTerminalLocked(ref, pending, errors.New("pending remote terminal reconciliation capacity exhausted"))
	}
	pending.terminals = append(pending.terminals, terminal)
	pending.seen[truth] = struct{}{}
	c.pendingRemoteTurnSubmits[ref] = pending
	c.pendingRemoteTerminalCount++
	return false, true, nil
}

func (c *turnController) failPendingRemoteTerminalLocked(ref remoteTurnSubmitRef, pending pendingRemoteTurnSubmit, err error) (bool, bool, error) {
	pending.err = err
	c.pendingRemoteTurnSubmits[ref] = pending
	return false, false, err
}
