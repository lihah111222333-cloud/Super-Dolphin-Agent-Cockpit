package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// TestHandleTerminalReceiptKnownOwnerMissingRuntimeRequiresRecovery ensures a
// durable owner alone cannot make a post-restart terminal look acknowledged.
func TestHandleTerminalReceiptKnownOwnerMissingRuntimeRequiresRecovery(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	svc.lifecycle.agentThreads = terminalOwnerLookupThreadStore{owner: "agent-1"}

	err := svc.HandleTerminalReceipt(context.Background(), remoteTerminalFixture("thread-1", "turn-1"))
	if !errors.Is(err, errRemoteTerminalRecoveryRequired) {
		t.Fatalf("HandleTerminalReceipt() error = %v, want recovery-required", err)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("missing-runtime receipt sealed terminal: entries=%d, want 0", got)
	}
}

// TestHandleTerminalReceiptMemoryBufferedRequiresRecovery keeps the strict
// receipt path from acknowledging a terminal that exists only in the
// in-process turn-submit buffer. A daemon crash would otherwise lose it.
func TestHandleTerminalReceiptMemoryBufferedRequiresRecovery(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	attempt := remoteTurnSubmitAttempt{
		agentID: agent.id, threadID: agent.remoteThreadID,
		turnID: agent.activeTurnID, launchSeq: agent.launchSeq,
	}
	if err := svc.turns.beginRemoteTurnSubmit(attempt); err != nil {
		t.Fatalf("beginRemoteTurnSubmit() error = %v", err)
	}

	err := svc.HandleTerminalReceipt(context.Background(), remoteTerminalFixture(agent.remoteThreadID, agent.activeTurnID))
	if !errors.Is(err, errRemoteTerminalRecoveryRequired) {
		t.Fatalf("HandleTerminalReceipt(buffered) error = %v, want recovery-required", err)
	}
	if got := svc.turns.pendingRemoteTerminalCount; got != 1 {
		t.Fatalf("pending remote terminal count = %d, want 1 retained for reconciliation", got)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("memory-buffered terminal entered first-truth seal: entries=%d, want 0", got)
	}
}

// TestHandleTerminalReceiptExactDuplicateAfterRuntimeRestart proves that an
// already durable public terminal is the only route-miss duplicate ACK.
func TestHandleTerminalReceiptExactDuplicateAfterRuntimeRestart(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	event := terminalOutcomeEvent(true)
	terminal, ok, err := turndto.CanonicalTurnTerminal(event)
	if err != nil || !ok {
		t.Fatalf("CanonicalTurnTerminal() = (%#v, %v, %v)", terminal, ok, err)
	}
	if handled, err := svc.CommitTurnCompleted(context.Background(), event); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled nil", handled, err)
	}
	svc.registry.lock()
	delete(svc.registry.agents, agent.id)
	svc.registry.unlock()
	svc.lifecycle.agentThreads = terminalOwnerLookupThreadStore{owner: agent.id}

	if err := svc.HandleTerminalReceipt(context.Background(), terminal); err != nil {
		t.Fatalf("exact duplicate HandleTerminalReceipt() error = %v, want nil", err)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("exact duplicate sealed terminal: entries=%d, want 0", got)
	}
}

// TestHandleTerminalReceiptStaleTurnAfterRuntimeRestart proves that an opaque
// turn mismatch is not enough stale evidence after a runtime restart.
func TestHandleTerminalReceiptStaleTurnAfterRuntimeRestart(t *testing.T) {
	svc, oldTerminal := prepareStaleTerminalScenario(t)

	if err := svc.HandleTerminalReceipt(context.Background(), oldTerminal); !errors.Is(err, errRemoteTerminalRecoveryRequired) {
		t.Fatalf("stale HandleTerminalReceipt() error = %v, want recovery-required", err)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("stale terminal sealed terminal: entries=%d, want 0", got)
	}
}

func prepareStaleTerminalScenario(t *testing.T) (*service, turndto.TurnTerminalV2) {
	t.Helper()
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	oldEvent := terminalOutcomeEvent(true)
	oldTerminal, ok, err := turndto.CanonicalTurnTerminal(oldEvent)
	if err != nil || !ok {
		t.Fatalf("CanonicalTurnTerminal(old) = (%#v, %v, %v)", oldTerminal, ok, err)
	}
	if handled, err := svc.CommitTurnCompleted(context.Background(), oldEvent); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted(old) = (%v, %v), want handled nil", handled, err)
	}
	newEvent := cloneTerminalTurnEvent(oldEvent)
	newEvent.TurnID = "turn-2"
	newTerminal, err := turndto.NewTurnTerminalV2(newEvent, "terminal:event-2")
	if err != nil {
		t.Fatalf("NewTurnTerminalV2(new) error = %v", err)
	}
	newEvent, err = turndto.AttachCanonicalTurnTerminal(newEvent, newTerminal)
	if err != nil {
		t.Fatalf("AttachCanonicalTurnTerminal(new) error = %v", err)
	}
	activateReplacementTurn(t, svc, agent)
	if handled, err := svc.CommitTurnCompleted(context.Background(), newEvent); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted(new) = (%v, %v), want handled nil", handled, err)
	}
	svc.registry.lock()
	delete(svc.registry.agents, agent.id)
	svc.registry.unlock()
	svc.lifecycle.agentThreads = terminalOwnerLookupThreadStore{owner: agent.id}
	return svc, oldTerminal
}

func cloneTerminalTurnEvent(oldEvent turndto.TurnCompleted) turndto.TurnCompleted {
	return turndto.TurnCompleted{TurnHeader: oldEvent.TurnHeader, Success: oldEvent.Success, Error: oldEvent.Error, Status: oldEvent.Status, Reason: oldEvent.Reason, Result: oldEvent.Result, Summary: oldEvent.Summary, Message: oldEvent.Message, StopReason: oldEvent.StopReason, TerminationRequestID: oldEvent.TerminationRequestID, PartialItemIDs: oldEvent.PartialItemIDs}
}

func activateReplacementTurn(t *testing.T, svc *service, agent *agentRuntime) {
	t.Helper()
	if err := svc.registry.withAgentLocked(agent.id, func(current *agentRuntime) error {
		current.activeTurnID = "turn-2"
		current.state = agentdto.StateTurnRunning
		head, err := svc.turns.activateTerminalTurnHeadLocked(context.Background(), current, current.activeTurnID, string(current.state))
		if err != nil {
			return err
		}
		current.terminalHeadVersion = head.Version
		return nil
	}); err != nil {
		t.Fatalf("activate replacement turn head: %v", err)
	}
}

// TestHandleTerminalReceiptFutureTurnBeforeRuntimeRehydrateRequiresRecovery
// prevents a not-yet-rehydrated future terminal from being mistaken for stale.
func TestHandleTerminalReceiptFutureTurnBeforeRuntimeRehydrateRequiresRecovery(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	if handled, err := svc.CommitTurnCompleted(context.Background(), terminalOutcomeEvent(true)); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted(current) = (%v, %v), want handled nil", handled, err)
	}
	svc.registry.lock()
	delete(svc.registry.agents, agent.id)
	svc.registry.unlock()
	svc.lifecycle.agentThreads = terminalOwnerLookupThreadStore{owner: agent.id}

	err := svc.HandleTerminalReceipt(context.Background(), remoteTerminalFixture("thread-1", "turn-2"))
	if !errors.Is(err, errRemoteTerminalRecoveryRequired) {
		t.Fatalf("future HandleTerminalReceipt() error = %v, want recovery-required", err)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("future terminal sealed terminal: entries=%d, want 0", got)
	}
}

// TestHandleTerminalReceiptPublicReadFailureCannotBeOverriddenByHead proves
// that a corrupt/unavailable public outcome never becomes an ACK merely
// because the current head still happens to contain the same identity.
func TestHandleTerminalReceiptPublicReadFailureCannotBeOverriddenByHead(t *testing.T) {
	svc, _ := newTerminalOutcomeTestService(t)
	agent := addTerminalOutcomeTestAgent(svc)
	event := terminalOutcomeEvent(true)
	terminal, ok, err := turndto.CanonicalTurnTerminal(event)
	if err != nil || !ok {
		t.Fatalf("CanonicalTurnTerminal() = (%#v, %v, %v)", terminal, ok, err)
	}
	if handled, err := svc.CommitTurnCompleted(context.Background(), event); err != nil || !handled {
		t.Fatalf("CommitTurnCompleted() = (%v, %v), want handled nil", handled, err)
	}
	base := svc.terminalOutcomes
	reader, ok := base.(contract.TerminalOutcomeHeadReadPort)
	if !ok {
		t.Fatal("terminal outcome store does not expose head reader")
	}
	svc.terminalOutcomes = terminalOutcomePublicReadErrorPort{
		TerminalOutcomeCommitPort: base,
		err:                       errors.New("public outcome integrity failure"),
	}
	svc.terminalHeadReader = reader
	svc.registry.lock()
	delete(svc.registry.agents, agent.id)
	svc.registry.unlock()
	svc.lifecycle.agentThreads = terminalOwnerLookupThreadStore{owner: agent.id}

	err = svc.HandleTerminalReceipt(context.Background(), terminal)
	if !errors.Is(err, errRemoteTerminalRecoveryRequired) {
		t.Fatalf("public-read failure HandleTerminalReceipt() error = %v, want recovery-required", err)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("public-read failure sealed terminal: entries=%d, want 0", got)
	}
}

// TestHandleTerminalReceiptMalformedHasNoSideEffects locks the strict ingress
// validation before owner lookup, durable reads, or terminal sealing.
func TestHandleTerminalReceiptMalformedHasNoSideEffects(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "thread-1", "thread-1"
	agent.activeTurnID = "turn-1"
	agent.lastReport = "before"
	svc.registry.agents[agent.id] = agent

	malformed := remoteTerminalFixture("thread-1", "turn-1")
	malformed.EventID = ""
	err := svc.HandleTerminalReceipt(context.Background(), malformed)
	if err == nil {
		t.Fatal("HandleTerminalReceipt(malformed) error = nil, want validation error")
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" || agent.lastReport != "before" {
		t.Fatalf("malformed terminal mutated runtime: state=%q turn=%q report=%q", agent.state, agent.activeTurnID, agent.lastReport)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("malformed terminal sealed terminal: entries=%d, want 0", got)
	}
}

type terminalOutcomePublicReadErrorPort struct {
	contract.TerminalOutcomeCommitPort
	err error
}

func (p terminalOutcomePublicReadErrorPort) GetPublicTerminalOutcome(context.Context, string) (contract.TerminalOutcomeCommit, error) {
	return contract.TerminalOutcomeCommit{}, p.err
}
