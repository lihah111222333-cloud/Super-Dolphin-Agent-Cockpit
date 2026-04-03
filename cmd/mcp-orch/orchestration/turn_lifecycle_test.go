package orchestration

import (
	"context"
	"testing"
	"time"

	"github.com/kelindar/event"
	"go.uber.org/fx"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shared "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
)

type stubLifecycle struct {
	hooks []fx.Hook
}

func (l *stubLifecycle) Append(hook fx.Hook) {
	l.hooks = append(l.hooks, hook)
}

func TestHandleTurnCompletedEventForcesIdleWhenActiveTurnMissing(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	svc.agents[agent.id] = agent

	handleTurnCompletedEvent(svc, silentLogger(), completedEvent("agent-1", "thread-1", "turn-1", false, "boom"))

	if agent.state != agentdto.StateIdle {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateIdle)
	}
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", agent.activeTurnID)
	}
	if agent.lastError != "boom" {
		t.Fatalf("lastError = %q, want boom", agent.lastError)
	}
}

func TestForceIdleAfterCompletionErrorKeepsDifferentActiveTurn(t *testing.T) {
	t.Parallel()

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "turn-active"
	svc.agents[agent.id] = agent

	recovered, err := svc.forceIdleAfterCompletionError(
		withEventTime(context.TODO(), time.Now()),
		"agent-1",
		"turn-finished",
		false,
		"boom",
	)
	if err == nil {
		t.Fatal("forceIdleAfterCompletionError() error = nil, want non-nil")
	}
	if recovered {
		t.Fatal("forceIdleAfterCompletionError() recovered = true, want false")
	}
	if agent.state != agentdto.StateTurnRunning {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnRunning)
	}
	if agent.activeTurnID != "turn-active" {
		t.Fatalf("activeTurnID = %q, want turn-active", agent.activeTurnID)
	}
}

func TestRegisterTurnLifecycleHandlesTurnInterrupted(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	startTurnLifecycle(t, dispatcher, svc)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.agents[agent.id] = agent

	interruptAt := time.Unix(1710000000, 0).UTC()
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", interruptAt))

	waitForAgentState(t, agent, agentdto.StateIdle)
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", agent.activeTurnID)
	}
	if agent.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", agent.lastError)
	}
	if !agent.updatedAt.Equal(interruptAt) {
		t.Fatalf("updatedAt = %s, want %s", agent.updatedAt, interruptAt)
	}
}

func TestHandleTurnInterruptedEventIsIdempotent(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	startTurnLifecycle(t, dispatcher, svc)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.agents[agent.id] = agent

	firstInterruptAt := time.Unix(1710000000, 0).UTC()
	secondInterruptAt := firstInterruptAt.Add(time.Minute)
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", firstInterruptAt))
	waitForAgentState(t, agent, agentdto.StateIdle)
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", secondInterruptAt))

	assertAgentUpdatedAtStays(t, agent, firstInterruptAt)
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", agent.activeTurnID)
	}
	if agent.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", agent.lastError)
	}
	if !agent.updatedAt.Equal(firstInterruptAt) {
		t.Fatalf("updatedAt = %s, want %s", agent.updatedAt, firstInterruptAt)
	}
}

func TestHandleTurnCompletedEventConvergesAfterInterrupt(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })

	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	startTurnLifecycle(t, dispatcher, svc)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.threadID = "thread-1"
	agent.activeTurnID = "turn-1"
	svc.agents[agent.id] = agent

	interruptAt := time.Unix(1710000000, 0).UTC()
	completedAt := interruptAt.Add(time.Minute)
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", interruptAt))
	waitForAgentState(t, agent, agentdto.StateIdle)
	event.Publish(dispatcher, completedEventAt("agent-1", "thread-1", "turn-1", true, "", completedAt))

	assertAgentUpdatedAtStays(t, agent, interruptAt)
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", agent.activeTurnID)
	}
	if agent.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", agent.lastError)
	}
	if !agent.updatedAt.Equal(interruptAt) {
		t.Fatalf("updatedAt = %s, want %s", agent.updatedAt, interruptAt)
	}
}

func completedEvent(agentID, threadID, turnID string, success bool, errMsg string) turndto.TurnCompleted {
	return completedEventAt(agentID, threadID, turnID, success, errMsg, time.Now())
}

func completedEventAt(agentID, threadID, turnID string, success bool, errMsg string, timestamp time.Time) turndto.TurnCompleted {
	return turndto.TurnCompleted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{
					EventHeader: shared.EventHeader{Timestamp: timestamp},
					ThreadID:    threadID,
				},
				AgentID: agentID,
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
		},
		Success: success,
		Error:   errMsg,
	}
}

func interruptedEventAt(agentID, threadID, turnID, reason string, timestamp time.Time) turndto.TurnInterrupted {
	return turndto.TurnInterrupted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{
					EventHeader: shared.EventHeader{Timestamp: timestamp},
					ThreadID:    threadID,
				},
				AgentID: agentID,
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: turnID},
		},
		Reason: reason,
	}
}

func startTurnLifecycle(t *testing.T, dispatcher *event.Dispatcher, svc *service) {
	t.Helper()

	lc := &stubLifecycle{}
	registerTurnLifecycle(lc, dispatcher, svc, silentLogger())
	if len(lc.hooks) != 1 {
		t.Fatalf("registerTurnLifecycle() hooks = %d, want 1", len(lc.hooks))
	}
	hook := lc.hooks[0]
	if hook.OnStart == nil {
		t.Fatal("registerTurnLifecycle() OnStart = nil")
	}
	if err := hook.OnStart(context.Background()); err != nil {
		t.Fatalf("OnStart() error = %v", err)
	}
	t.Cleanup(func() {
		if hook.OnStop != nil {
			if err := hook.OnStop(context.Background()); err != nil {
				t.Errorf("OnStop() error = %v", err)
			}
		}
	})
}

func waitForAgentState(t *testing.T, agent *agentRuntime, wantState string) {
	t.Helper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		if agent.state == wantState {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("agent.state = %q, want %q", agent.state, wantState)
}

func assertAgentUpdatedAtStays(t *testing.T, agent *agentRuntime, want time.Time) {
	t.Helper()

	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		if !agent.updatedAt.Equal(want) {
			t.Fatalf("updatedAt = %s, want %s", agent.updatedAt, want)
		}
		time.Sleep(time.Millisecond)
	}
}
