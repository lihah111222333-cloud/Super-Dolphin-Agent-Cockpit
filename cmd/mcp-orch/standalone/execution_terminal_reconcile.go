package standalone

import (
	"context"
	"errors"
	"reflect"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// ReconcileTerminalReceipt 精确认领旧会话保留的canonical回执，仅重交付同一真相，不发起provider操作。
func (e *Execution) ReconcileTerminalReceipt(ctx context.Context, agentID string, threadID string, launchSeq uint64, terminal turndto.TurnTerminalV2) error {
	if err := requireActiveExecutionContext(ctx); err != nil {
		return err
	}
	if agentID == "" || threadID == "" || launchSeq == 0 {
		return errors.New("terminal receipt reconciliation requires complete session identity")
	}
	if err := turndto.ValidateTurnTerminalV2(terminal); err != nil {
		return err
	}
	pending, session, err := e.claimReconciledTerminal(ctx, agentID, threadID, launchSeq, terminal)
	if err != nil {
		return err
	}
	return e.finishTerminalReceipt(session, pending)
}

// claimReconciledTerminal 只lookup一次并在相同锁区核对原session与完整canonical值，禁止旧调用认领新代际。
func (e *Execution) claimReconciledTerminal(ctx context.Context, agentID, threadID string, launchSeq uint64, terminal turndto.TurnTerminalV2) (*pendingTerminalReceipt, *cliSession, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	session := e.sessions[agentID]
	if session == nil {
		return nil, nil, contract.ErrAgentNotFound
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	if session.request.AgentID != agentID || session.threadID != threadID || session.launchSeq != launchSeq || terminal.ThreadID != threadID {
		return nil, nil, errors.New("terminal receipt reconciliation session identity mismatch")
	}
	pending := session.pendingTerminal
	if pending == nil {
		return nil, nil, errCLITerminalReceiptMissing
	}
	if !reflect.DeepEqual(pending.terminal, terminal) {
		return nil, nil, errors.New("terminal receipt reconciliation canonical truth mismatch")
	}
	if pending.inFlight {
		return nil, nil, errCLITerminalReceiptInFlight
	}
	pending.inFlight, pending.retryable = true, false
	return pending, session, nil
}
