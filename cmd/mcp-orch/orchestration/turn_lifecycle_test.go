package orchestration

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
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

type agentSnapshot struct {
	state        string
	activeTurnID string
	lastError    string
	updatedAt    time.Time
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

	snapshot := waitForAgentState(t, svc, agent.id, agentdto.StateIdle)
	if snapshot.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", snapshot.activeTurnID)
	}
	if snapshot.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", snapshot.lastError)
	}
	if !snapshot.updatedAt.Equal(interruptAt) {
		t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, interruptAt)
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
	waitForAgentState(t, svc, agent.id, agentdto.StateIdle)
	event.Publish(dispatcher, interruptedEventAt("agent-1", "thread-1", "turn-1", "user_cancelled", secondInterruptAt))

	assertAgentUpdatedAtStays(t, svc, agent.id, firstInterruptAt)
	snapshot := readAgentSnapshot(t, svc, agent.id)
	if snapshot.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", snapshot.activeTurnID)
	}
	if snapshot.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", snapshot.lastError)
	}
	if !snapshot.updatedAt.Equal(firstInterruptAt) {
		t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, firstInterruptAt)
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
	waitForAgentState(t, svc, agent.id, agentdto.StateIdle)
	event.Publish(dispatcher, completedEventAt("agent-1", "thread-1", "turn-1", true, "", completedAt))

	assertAgentUpdatedAtStays(t, svc, agent.id, interruptAt)
	snapshot := readAgentSnapshot(t, svc, agent.id)
	if snapshot.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty", snapshot.activeTurnID)
	}
	if snapshot.lastError != "user_cancelled" {
		t.Fatalf("lastError = %q, want user_cancelled", snapshot.lastError)
	}
	if !snapshot.updatedAt.Equal(interruptAt) {
		t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, interruptAt)
	}
}

func TestLogTurnCompletionFailureDowngradesAgentNotFound(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTurnCompletionFailure(logger, completedEvent("agent-1", "thread-1", "turn-1", false, ""), errAgentNotFound, false, nil)

	output := buf.String()
	if !strings.Contains(output, "level=DEBUG") {
		t.Fatalf("output = %q, want DEBUG", output)
	}
	if strings.Contains(output, "level=WARN") {
		t.Fatalf("output = %q, want no WARN", output)
	}
}

func TestLogTurnCompletionFailureKeepsUnexpectedErrorsWarn(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	logTurnCompletionFailure(logger, completedEvent("agent-1", "thread-1", "turn-1", false, ""), errors.New("boom"), false, nil)

	if output := buf.String(); !strings.Contains(output, "level=WARN") {
		t.Fatalf("output = %q, want WARN", output)
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
	RegisterTurnLifecycle(lc, dispatcher, svc, silentLogger())
	if len(lc.hooks) != 1 {
		t.Fatalf("RegisterTurnLifecycle() hooks = %d, want 1", len(lc.hooks))
	}
	hook := lc.hooks[0]
	if hook.OnStart == nil {
		t.Fatal("RegisterTurnLifecycle() OnStart = nil")
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

func waitForAgentState(t *testing.T, svc *service, agentID, wantState string) agentSnapshot {
	t.Helper()

	deadline := time.Now().Add(200 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshot := readAgentSnapshot(t, svc, agentID)
		if snapshot.state == wantState {
			return snapshot
		}
		time.Sleep(time.Millisecond)
	}
	snapshot := readAgentSnapshot(t, svc, agentID)
	t.Fatalf("agent.state = %q, want %q", snapshot.state, wantState)
	return agentSnapshot{}
}

func assertAgentUpdatedAtStays(t *testing.T, svc *service, agentID string, want time.Time) {
	t.Helper()

	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		snapshot := readAgentSnapshot(t, svc, agentID)
		if !snapshot.updatedAt.Equal(want) {
			t.Fatalf("updatedAt = %s, want %s", snapshot.updatedAt, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func readAgentSnapshot(t *testing.T, svc *service, agentID string) agentSnapshot {
	t.Helper()

	var snapshot agentSnapshot
	if err := svc.withAgentReadLocked(agentID, func(agent *agentRuntime) error {
		snapshot = agentSnapshot{
			state:        agent.state,
			activeTurnID: agent.activeTurnID,
			lastError:    agent.lastError,
			updatedAt:    agent.updatedAt,
		}
		return nil
	}); err != nil {
		t.Fatalf("withAgentReadLocked(%q) error = %v", agentID, err)
	}
	return snapshot
}
