package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/taskdag"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
)

func TestRecoverReplaysStoreBackedActiveTurnAheadOfQueuedWork(t *testing.T) {
	t.Parallel()

	svc, agent := newRecoverReplayService(t)
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	assertRecoveredReplayQueue(t, agent)
}

func newRecoverReplayService(t *testing.T) (*service, *agentRuntime) {
	t.Helper()
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.recoveryStore = stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     "agent-1",
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{
			7: {
				ID:            7,
				Status:        "sent",
				TargetAgentID: "agent-1",
				BoundTurnID:   testStringPtr("turn-active"),
				TurnBoundAt:   testTimePtr(t),
				PromptPayload: json.RawMessage(`{"agentId":"agent-1","prompt":"replay me","selectedSkills":["debug"]}`),
			},
		},
	}

	agent := svc.newAgentLocked("agent-1")
	agent.command = []string{"sh", "-c", "sleep 60"}
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.queue.Enqueue(TurnSubmission{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Inputs:   []shareddto.InputItem{{Type: "text", Content: "queued work"}},
	})
	svc.agents[agent.id] = agent
	t.Cleanup(func() { cleanupAgentProcess(agent) })
	return svc, agent
}

func assertRecoveredReplayQueue(t *testing.T, agent *agentRuntime) {
	t.Helper()
	if agent.state != agentdto.StateTurnQueued {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnQueued)
	}
	if got := agent.queue.Len(); got != 2 {
		t.Fatalf("queue.Len() = %d, want 2", got)
	}
	assertFirstRecoveredReplayTurn(t, dequeueRecoveredTurn(t, agent, "first"))
	assertSecondRecoveredReplayTurn(t, dequeueRecoveredTurn(t, agent, "second"))
}

func dequeueRecoveredTurn(t *testing.T, agent *agentRuntime, label string) TurnSubmission {
	t.Helper()
	turn, ok := agent.queue.Dequeue()
	if !ok {
		t.Fatalf("%s Dequeue() ok = false, want true", label)
	}
	return turn
}

func assertFirstRecoveredReplayTurn(t *testing.T, first TurnSubmission) {
	t.Helper()
	if first.ExpectedTurnID != "turn-active" {
		t.Fatalf("first.ExpectedTurnID = %q, want turn-active", first.ExpectedTurnID)
	}
	if first.ThreadID != "thread-1" {
		t.Fatalf("first.ThreadID = %q, want thread-1", first.ThreadID)
	}
	if len(first.Inputs) != 1 || first.Inputs[0].Content != "replay me" {
		t.Fatalf("first.Inputs = %#v, want replayed prompt", first.Inputs)
	}
}

func assertSecondRecoveredReplayTurn(t *testing.T, second TurnSubmission) {
	t.Helper()
	if len(second.Inputs) != 1 || second.Inputs[0].Content != "queued work" {
		t.Fatalf("second.Inputs = %#v, want queued work", second.Inputs)
	}
}

func cleanupAgentProcess(agent *agentRuntime) {
	if agent.cmd != nil {
		_ = stopProcess(agent.cmd)
	}
}

func TestRecoverPublishesTurnResumedForRecoveredTurn(t *testing.T) {
	t.Parallel()
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	svc.recoveryStore = replayableRecoveryTurnStore()
	resumedEvents := make(chan turndto.TurnResumed, 1)
	cancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) {
		resumedEvents <- ev
	})
	t.Cleanup(cancel)
	agent := svc.newAgentLocked("agent-1")
	agent.command = []string{"sh", "-c", "sleep 60"}
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.updatedAt = time.Unix(1710000000, 0).UTC()
	svc.agents[agent.id] = agent
	t.Cleanup(func() {
		if agent.cmd != nil {
			_ = stopProcess(agent.cmd)
		}
	})
	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	ev := awaitTurnResumed(t, resumedEvents)
	if ev.AgentID != "agent-1" {
		t.Fatalf("TurnResumed.AgentID = %q, want agent-1", ev.AgentID)
	}
	if ev.ThreadID != "thread-1" {
		t.Fatalf("TurnResumed.ThreadID = %q, want thread-1", ev.ThreadID)
	}
	if ev.TurnID != "turn-active" {
		t.Fatalf("TurnResumed.TurnID = %q, want turn-active", ev.TurnID)
	}
	if ev.Reason != turnResumeReasonRecover {
		t.Fatalf("TurnResumed.Reason = %q, want %q", ev.Reason, turnResumeReasonRecover)
	}
}

func TestRecoverWithoutActiveTurnDoesNotPublishTurnResumed(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	resumedEvents := make(chan turndto.TurnResumed, 1)
	cancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) { resumedEvents <- ev })
	t.Cleanup(cancel)
	agent := svc.newAgentLocked("agent-1")
	agent.command = []string{"sh", "-c", "sleep 60"}
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	svc.agents[agent.id] = agent
	t.Cleanup(func() {
		if agent.cmd != nil {
			_ = stopProcess(agent.cmd)
		}
	})

	if err := svc.Recover(context.Background(), agent.id); err != nil {
		t.Fatalf("Recover() error = %v", err)
	}
	select {
	case ev := <-resumedEvents:
		t.Fatalf("unexpected TurnResumed event: %+v", ev)
	case <-time.After(150 * time.Millisecond):
	}
}

func TestRecoverStalledAgentsPublishesTurnStalledAndResumed(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	svc.recoveryStore = replayableRecoveryTurnStore()
	stalledEvents := make(chan turndto.TurnStalled, 1)
	resumedEvents := make(chan turndto.TurnResumed, 1)
	stalledCancel := event.Subscribe(dispatcher, func(ev turndto.TurnStalled) {
		stalledEvents <- ev
	})
	resumedCancel := event.Subscribe(dispatcher, func(ev turndto.TurnResumed) {
		resumedEvents <- ev
	})
	t.Cleanup(stalledCancel)
	t.Cleanup(resumedCancel)

	agent := svc.newAgentLocked("agent-1")
	agent.command = []string{"sh", "-c", "sleep 60"}
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-active"
	agent.updatedAt = time.Now().Add(-time.Minute)
	svc.agents[agent.id] = agent
	t.Cleanup(func() { cleanupAgentProcess(agent) })

	actor := &runnerActor{logger: silentLogger(), service: svc}
	actor.recoverStalledAgents(context.Background(), &StallDetector{
		threshold: 30 * time.Second,
		logger:    silentLogger(),
	})

	stalled := awaitTurnStalled(t, stalledEvents)
	assertRecoverStalledEvent(t, stalled)

	resumed := awaitTurnResumed(t, resumedEvents)
	assertRecoverResumedEvent(t, resumed)
}

func assertRecoverStalledEvent(t *testing.T, stalled turndto.TurnStalled) {
	t.Helper()
	if stalled.AgentID != "agent-1" {
		t.Fatalf("TurnStalled.AgentID = %q, want agent-1", stalled.AgentID)
	}
	if stalled.ThreadID != "thread-1" {
		t.Fatalf("TurnStalled.ThreadID = %q, want thread-1", stalled.ThreadID)
	}
	if stalled.TurnID != "turn-active" {
		t.Fatalf("TurnStalled.TurnID = %q, want turn-active", stalled.TurnID)
	}
	if stalled.Reason != recoverReasonStall {
		t.Fatalf("TurnStalled.Reason = %q, want %q", stalled.Reason, recoverReasonStall)
	}
	if stalled.StalledMS < 30_000 {
		t.Fatalf("TurnStalled.StalledMS = %d, want >= 30000", stalled.StalledMS)
	}
}

func assertRecoverResumedEvent(t *testing.T, resumed turndto.TurnResumed) {
	t.Helper()
	if resumed.AgentID != "agent-1" {
		t.Fatalf("TurnResumed.AgentID = %q, want agent-1", resumed.AgentID)
	}
	if resumed.ThreadID != "thread-1" {
		t.Fatalf("TurnResumed.ThreadID = %q, want thread-1", resumed.ThreadID)
	}
	if resumed.TurnID != "turn-active" {
		t.Fatalf("TurnResumed.TurnID = %q, want turn-active", resumed.TurnID)
	}
	if resumed.Reason != turnResumeReasonRecover {
		t.Fatalf("TurnResumed.Reason = %q, want %q", resumed.Reason, turnResumeReasonRecover)
	}
}

func TestLoadRecoveredTurnSubmissionSkipsReclaimedWakeup(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	svc.recoveryStore = stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     "agent-1",
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{
			7: {
				ID:            7,
				Status:        "pending",
				TargetAgentID: "agent-1",
				PromptPayload: json.RawMessage(`{"agentId":"agent-1","prompt":"stale"}`),
			},
		},
	}
	agent := &agentRuntime{id: "agent-1", threadID: "thread-1", activeTurnID: "turn-active"}

	submission, shouldReplay, err := loadRecoveredTurnSubmission(context.Background(), svc, agent)
	if err != nil {
		t.Fatalf("loadRecoveredTurnSubmission() error = %v", err)
	}
	if shouldReplay {
		t.Fatalf("shouldReplay = true, want false with reclaimed wakeup: %#v", submission)
	}
	if got := len(submission.Inputs); got != 0 {
		t.Fatalf("submission.Inputs len = %d, want 0", got)
	}
}

func TestFireOrForceLockedIncludesContextForIllegalTransition(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateIdle

	err := svc.fireOrForceLocked(context.Background(), agent, agentdto.TriggerTurnAccepted)
	if err == nil {
		t.Fatal("fireOrForceLocked() error = nil, want non-nil")
	}
	for _, want := range []string{
		`state=idle`,
		`trigger=turn_accepted`,
		`turn_enqueued`,
		`recover_requested`,
		`stop_requested`,
		`process_exited`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fireOrForceLocked() error = %q, want substring %q", err, want)
		}
	}
}

func TestFireOrForceLockedIncludesContextWhenStateMachineMissing(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := &agentRuntime{id: "agent-1", state: agentdto.StateIdle}

	err := svc.fireOrForceLocked(context.Background(), agent, agentdto.TriggerTurnAccepted)
	if err == nil {
		t.Fatal("fireOrForceLocked() error = nil, want non-nil")
	}
	for _, want := range []string{
		`state=idle`,
		`trigger=turn_accepted`,
		`turn_enqueued`,
		`state machine is not initialized`,
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("fireOrForceLocked() error = %q, want substring %q", err, want)
		}
	}
}

type stubRecoveryTurnStore struct {
	nodes   []taskdag.Node
	wakeups map[int64]taskdag.Wakeup
}

func replayableRecoveryTurnStore() stubRecoveryTurnStore {
	return stubRecoveryTurnStore{
		nodes: []taskdag.Node{{
			DagKey:         "dag-1",
			NodeKey:        "node-1",
			AssignedTo:     "agent-1",
			ActiveTurnID:   testStringPtr("turn-active"),
			ActiveWakeupID: testInt64Ptr(7),
		}},
		wakeups: map[int64]taskdag.Wakeup{
			7: {
				ID:            7,
				Status:        "sent",
				TargetAgentID: "agent-1",
				BoundTurnID:   testStringPtr("turn-active"),
				TurnBoundAt:   testTimePtrValue(),
				PromptPayload: json.RawMessage(`{"agentId":"agent-1","prompt":"replay me"}`),
			},
		},
	}
}

func (s stubRecoveryTurnStore) ListRunningNodesByAssignee(_ context.Context, assignee string) ([]taskdag.Node, error) {
	filtered := make([]taskdag.Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		if node.AssignedTo == assignee {
			filtered = append(filtered, node)
		}
	}
	return filtered, nil
}

func (s stubRecoveryTurnStore) GetWakeup(_ context.Context, id int64) (*taskdag.Wakeup, error) {
	wakeup, ok := s.wakeups[id]
	if !ok {
		return nil, errors.New("wakeup not found")
	}
	return &wakeup, nil
}

func testStringPtr(value string) *string { return &value }

func testInt64Ptr(value int64) *int64 { return &value }

func testTimePtr(t *testing.T) *time.Time {
	t.Helper()
	return testTimePtrValue()
}

func testTimePtrValue() *time.Time {
	now := time.Date(2026, 3, 25, 12, 0, 0, 0, time.UTC)
	return &now
}

func awaitTurnStalled(t *testing.T, events <-chan turndto.TurnStalled) turndto.TurnStalled {
	t.Helper()

	select {
	case ev := <-events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TurnStalled event")
		var zero turndto.TurnStalled
		return zero
	}
}

func awaitTurnResumed(t *testing.T, events <-chan turndto.TurnResumed) turndto.TurnResumed {
	t.Helper()

	select {
	case ev := <-events:
		return ev
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for TurnResumed event")
		var zero turndto.TurnResumed
		return zero
	}
}
