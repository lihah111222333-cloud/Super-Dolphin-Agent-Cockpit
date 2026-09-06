package orchestration

import (
	"context"
	"testing"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// TestHandleTerminalReceiptFailsClosedWhenOwnerIsUnknown 保证 provider sink 能观察到
// 未绑定 thread 的终态错误；兼容的 HandleTerminal 仍只负责记录并丢弃该错误。
func TestHandleTerminalReceiptFailsClosedWhenOwnerIsUnknown(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	err := svc.HandleTerminalReceipt(context.Background(), turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-1",
		ThreadID:      "unknown-thread",
		TurnID:        "turn-1",
		Outcome:       "success",
		PublicSummary: "done",
		OccurredAt:    "2026-07-17T01:02:03Z",
	})
	if err == nil {
		t.Fatal("HandleTerminalReceipt() error = nil, want unknown owner failure")
	}
	// The legacy void sink remains safe for notification callers.
	svc.HandleTerminal(context.Background(), turndto.TurnTerminalV2{ThreadID: "unknown-thread", TurnID: "turn-1"})
}

// TestHandleTerminalReceiptFailsClosedWhenDurableStoreIsMissing 锁定未接线
// durable owner 时不得经由 legacy 内存 report/runtime 路径 ACK canonical terminal。
func TestHandleTerminalReceiptFailsClosedWhenDurableStoreIsMissing(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "thread-1", "thread-1"
	agent.activeTurnID = "turn-1"
	svc.registry.agents[agent.id] = agent

	err := svc.HandleTerminalReceipt(context.Background(), turndto.TurnTerminalV2{
		SchemaVersion: 2,
		EventID:       "event-1",
		ThreadID:      "thread-1",
		TurnID:        "turn-1",
		Outcome:       "success",
		PublicSummary: "done",
		OccurredAt:    "2026-07-17T01:02:03Z",
	})
	if err == nil {
		t.Fatal("HandleTerminalReceipt() error = nil, want missing durable store failure")
	}
	if agent.state != agentdto.StateTurnRunning || agent.activeTurnID != "turn-1" || agent.lastReport != "" {
		t.Fatalf("receipt without durable store mutated runtime: state=%q turn=%q report=%q", agent.state, agent.activeTurnID, agent.lastReport)
	}
	if got := svc.turns.remoteTerminalSealStats().Entries; got != 0 {
		t.Fatalf("receipt without durable store sealed terminal: entries=%d, want 0", got)
	}
}

// TestHandleRemoteTurnCompletedFailsClosedWhenKnownOwnerHasNoStore covers the
// canonical result path directly. A known in-memory owner must not cause the
// false,nil legacy result from CommitTurnCompleted to fall through into the
// report/runtime lifecycle, which would acknowledge the provider terminal.
func TestHandleRemoteTurnCompletedFailsClosedWhenKnownOwnerHasNoStore(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID, agent.remoteThreadID = "thread-1", "thread-1"
	agent.activeTurnID = "turn-1"
	agent.lastReport = "before"
	agent.reportRequesters = []string{"requester-1"}
	svc.registry.agents[agent.id] = agent

	beforeState, beforeTurn, beforeReport := agent.state, agent.activeTurnID, agent.lastReport
	beforeRequesters := append([]string(nil), agent.reportRequesters...)
	if err := svc.handleRemoteTurnCompletedResult(context.Background(), terminalOutcomeEvent(true)); err == nil {
		t.Fatal("handleRemoteTurnCompletedResult() error = nil, want missing canonical terminal owner")
	}
	if agent.state != beforeState || agent.activeTurnID != beforeTurn || agent.lastReport != beforeReport ||
		len(agent.reportRequesters) != len(beforeRequesters) {
		t.Fatalf("known-owner/no-store terminal mutated runtime: state=%q turn=%q report=%q requesters=%v",
			agent.state, agent.activeTurnID, agent.lastReport, agent.reportRequesters)
	}
	for i := range beforeRequesters {
		if agent.reportRequesters[i] != beforeRequesters[i] {
			t.Fatalf("known-owner/no-store terminal mutated requesters: got=%v want=%v", agent.reportRequesters, beforeRequesters)
		}
	}
}

// TestRemoteTerminalRetryAllowsExactRetry prevents a failed projection from
// permanently sealing the provider terminal before its durable commit succeeds.
func TestRemoteTerminalRetryAllowsExactRetry(t *testing.T) {
	controller := &turnController{}
	terminal := remoteTerminalFixture("thread-retry", "turn-1")
	accepted, err := controller.acceptRemoteTurnTerminal(terminal)
	if err != nil || !accepted.accepted {
		t.Fatalf("initial terminal acceptance = %#v, %v", accepted, err)
	}
	controller.retryRemoteTurnTerminal(accepted)
	retry, err := controller.acceptRemoteTurnTerminal(terminal)
	if err != nil || !retry.accepted {
		t.Fatalf("exact terminal retry = %#v, %v", retry, err)
	}
	controller.releaseRemoteTurnTerminal(retry)
	conflict := terminal
	conflict.EventID = "event-conflict"
	if conflicting, err := controller.acceptRemoteTurnTerminal(conflict); err == nil || conflicting.accepted {
		t.Fatalf("conflicting terminal after retry = %#v, %v; want rejection", conflicting, err)
	}
}
