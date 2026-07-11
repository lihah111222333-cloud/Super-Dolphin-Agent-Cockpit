package orchestration

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

// recordingNotifyTap captures every fired tap call for assertion.
type recordingNotifyTap struct {
	mu          sync.Mutex
	completed   []turndto.TurnCompleted
	interrupted []turndto.TurnInterrupted
	stopped     []threaddto.Stopped
	count       atomic.Int64
}

func (r *recordingNotifyTap) OnTurnCompleted(_ context.Context, ev turndto.TurnCompleted) {
	r.mu.Lock()
	r.completed = append(r.completed, ev)
	r.mu.Unlock()
	r.count.Add(1)
}
func (r *recordingNotifyTap) OnTurnInterrupted(_ context.Context, ev turndto.TurnInterrupted) {
	r.mu.Lock()
	r.interrupted = append(r.interrupted, ev)
	r.mu.Unlock()
	r.count.Add(1)
}
func (r *recordingNotifyTap) OnThreadStopped(_ context.Context, ev threaddto.Stopped) {
	r.mu.Lock()
	r.stopped = append(r.stopped, ev)
	r.mu.Unlock()
	r.count.Add(1)
}

// hookConsumerWithTap builds a hookConsumer with the supplied tap using
// the same service + logger wiring the rest of hook_consumer_test.go
// uses. Tap may be nil — tests cover both paths.
func hookConsumerWithTap(t *testing.T, tap NotifyTap) (*hookConsumer, *service) {
	t.Helper()
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	return newHookConsumerInternal(svc, silentLogger(), tap, nil, nil), svc
}

// TestHookConsumerFiresTapOnTurnCompleted verifies the post-handler
// tap path runs and carries the original event payload through.
func TestHookConsumerFiresTapOnTurnCompleted(t *testing.T) {
	tap := &recordingNotifyTap{}
	hc, svc := hookConsumerWithTap(t, tap)
	addHookTestAgent(t, svc, "agent-1")
	ev := turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: true,
		Result:  "ok",
	}
	hc.handleTurnCompleted(context.Background(), ev)
	if got := tap.count.Load(); got != 1 {
		t.Fatalf("tap fired %d times, want 1", got)
	}
	if len(tap.completed) != 1 || tap.completed[0].TurnID != "turn-1" {
		t.Fatalf("captured event wrong: %+v", tap.completed)
	}
}

func TestHookConsumerFiresTapOnTurnInterrupted(t *testing.T) {
	tap := &recordingNotifyTap{}
	hc, svc := hookConsumerWithTap(t, tap)
	addHookTestAgent(t, svc, "agent-1")
	ev := turndto.TurnInterrupted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
		},
		Reason: "user",
	}
	hc.handleTurnInterrupted(context.Background(), ev)
	if len(tap.interrupted) != 1 {
		t.Fatalf("want 1 interrupted tap, got %d", len(tap.interrupted))
	}
}

func TestHookConsumerFiresTapOnThreadStopped(t *testing.T) {
	tap := &recordingNotifyTap{}
	hc, svc := hookConsumerWithTap(t, tap)
	addHookTestAgent(t, svc, "agent-1")
	ev := threaddto.Stopped{ThreadID: "thread-1", AgentID: "agent-1", Reason: "process_exit"}
	hc.handleThreadStopped(context.Background(), ev)
	if len(tap.stopped) != 1 {
		t.Fatalf("want 1 stopped tap, got %d", len(tap.stopped))
	}
}

func TestThreadStoppedHookDoesNotRepublishAlreadyStoppedAgent(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	stopped := make(chan agentdto.AgentStopped, 1)
	cancel := event.Subscribe(dispatcher, func(ev agentdto.AgentStopped) {
		stopped <- ev
	})
	defer cancel()
	svc := NewService(silentLogger(), dispatcher, nil, nil, nil, nil)
	hc := newHookConsumerInternal(svc, silentLogger(), nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateStopped
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	svc.registry.agents[agent.id] = agent

	hc.handleThreadStopped(context.Background(), threaddto.Stopped{ThreadID: "thread-1", AgentID: "agent-1", Reason: "remote_stopped"})

	select {
	case ev := <-stopped:
		t.Fatalf("unexpected duplicate AgentStopped event: %#v", ev)
	case <-time.After(20 * time.Millisecond):
	}
}

// TestHookConsumerNilTapIsNoop exercises the fast path where no tap is
// wired; the consumer must still complete its primary work without
// panicking.
func TestHookConsumerNilTapIsNoop(t *testing.T) {
	hc, svc := hookConsumerWithTap(t, nil)
	addHookTestAgent(t, svc, "agent-1")
	hc.handleTurnCompleted(context.Background(), turndto.TurnCompleted{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader:  shareddto.AgentHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"},
			TurnIDHeader: shareddto.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: true,
	})
	// Nothing to assert beyond "did not panic". If the tap call was
	// unconditional the nil interface method call would have panicked
	// immediately.
}
