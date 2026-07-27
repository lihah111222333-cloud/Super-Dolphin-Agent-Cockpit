package wails

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runtimesafe"
	"go.uber.org/fx/fxtest"
)

func TestBindEventBridgePropagatesInvalidDependencyOnStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		dispatch  *event.Dispatcher
		lifecycle *WailsLifecycle
		want      string
	}{
		{
			name:      "nil dispatcher",
			lifecycle: lifecycleWithTestEmitter(),
			want:      "dispatcher",
		},
		{
			name:     "nil lifecycle",
			dispatch: event.NewDispatcher(),
			want:     "lifecycle",
		},
		{
			name:      "nil emitter",
			dispatch:  event.NewDispatcher(),
			lifecycle: NewWailsLifecycle(nil, nil),
			want:      "emitter",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lc := fxtest.NewLifecycle(t)
			bridge := NewEventBridge(tc.dispatch, tc.lifecycle, nil)
			bindEventBridge(lc, bridge)

			err := lc.Start(context.Background())
			if err == nil {
				t.Fatalf("Lifecycle.Start() error = nil, want %s dependency failure", tc.want)
			}
			if !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("Lifecycle.Start() error = %v, want containing %q", err, tc.want)
			}
			if tc.dispatch != nil {
				_ = tc.dispatch.Close()
			}
		})
	}
}

func TestEventBridgeStopDrainsInflightAndRestartDoesNotDuplicate(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	lifecycle := NewWailsLifecycle(nil, nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	var blockOnce sync.Once
	var emittedMu sync.Mutex
	emitted := 0
	lifecycle.SetEventEmitter(func(name string, _ any) {
		if name != bridgeEventName {
			return
		}
		blockOnce.Do(func() {
			close(entered)
			<-release
		})
		emittedMu.Lock()
		emitted++
		emittedMu.Unlock()
	})

	bridge := NewEventBridge(dispatcher, lifecycle, nil)
	requireEventBridgeStart(t, bridge, "Start()")
	requireEventBridgeStart(t, bridge, "second Start()")
	event.Publish(dispatcher, threaddto.Started{ThreadID: "thread-1", AgentID: "agent-1"})
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("event callback did not enter emitter")
	}

	stopped := make(chan struct{})
	runtimesafe.SafeGo(t.Context(), nil, "wails.test.event-bridge-stop", func(context.Context) {
		bridge.Stop()
		close(stopped)
	})
	select {
	case <-stopped:
		t.Fatal("Stop() returned before in-flight event completed")
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop() did not return after in-flight event completed")
	}

	waitBridgeEmissionCount(t, &emittedMu, &emitted, 3)
	event.Publish(dispatcher, threaddto.Started{ThreadID: "thread-stopped", AgentID: "agent-stopped"})
	time.Sleep(20 * time.Millisecond)
	assertBridgeEmissionCount(t, &emittedMu, &emitted, 3)

	requireEventBridgeStart(t, bridge, "restart Start()")
	event.Publish(dispatcher, threaddto.Started{ThreadID: "thread-2", AgentID: "agent-2"})
	waitBridgeEmissionCount(t, &emittedMu, &emitted, 6)
	bridge.Stop()
	assertBridgeEmissionCount(t, &emittedMu, &emitted, 6)
}

func requireEventBridgeStart(t *testing.T, bridge *EventBridge, operation string) {
	t.Helper()
	if err := bridge.Start(); err != nil {
		t.Fatalf("%s error = %v", operation, err)
	}
}

func waitBridgeEmissionCount(t *testing.T, mu *sync.Mutex, emitted *int, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := *emitted
		mu.Unlock()
		if got == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	got := *emitted
	mu.Unlock()
	t.Fatalf("emitted bridge event count = %d, want %d", got, want)
}

func assertBridgeEmissionCount(t *testing.T, mu *sync.Mutex, emitted *int, want int) {
	t.Helper()
	mu.Lock()
	got := *emitted
	mu.Unlock()
	if got != want {
		t.Fatalf("emitted bridge event count = %d, want %d", got, want)
	}
}

func lifecycleWithTestEmitter() *WailsLifecycle {
	lifecycle := NewWailsLifecycle(nil, nil)
	lifecycle.SetEventEmitter(func(string, any) {})
	return lifecycle
}

func TestEventBridgePublishEmitsLegacyRefreshEvents(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	emitted := make([]map[string]any, 0, 3)
	lifecycle.SetEventEmitter(func(name string, payload any) {
		if name != bridgeEventName {
			return
		}
		value, _ := payload.(map[string]any)
		emitted = append(emitted, value)
	})

	bridge := NewEventBridge(nil, lifecycle, nil)
	bridge.publish("thread/started", map[string]any{
		"threadId": "thread-1",
		"agentId":  "agent-1",
	})

	if len(emitted) != 3 {
		t.Fatalf("len(emitted) = %d, want 3", len(emitted))
	}
	assertBridgeType(t, emitted[0], "thread/started")
	assertBridgeType(t, emitted[1], "ui/thread/changed")
	assertBridgeType(t, emitted[2], "ui/sidebar/changed")
}

func TestEventBridgePublishEmitsAgentEventCompatibilityChannel(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	emitted := make([]map[string]any, 0, 3)
	lifecycle.SetEventEmitter(func(name string, payload any) {
		if name != agentEventName {
			return
		}
		value, _ := payload.(map[string]any)
		emitted = append(emitted, value)
	})

	bridge := NewEventBridge(nil, lifecycle, nil)
	bridge.publish("thread/started", map[string]any{
		"threadId": "thread-1",
		"agentId":  "agent-1",
	})

	if len(emitted) != 3 {
		t.Fatalf("len(emitted) = %d, want 3", len(emitted))
	}
	assertAgentEvent(t, emitted[0], "thread-1", "thread/started")
	assertAgentEvent(t, emitted[1], "thread-1", "ui/thread/changed")
	assertAgentEvent(t, emitted[2], "thread-1", "ui/sidebar/changed")
}

func TestEventBridgePublishSkipsAgentEventWithoutIdentity(t *testing.T) {
	t.Parallel()

	lifecycle := NewWailsLifecycle(nil, nil)
	agentEvents := 0
	lifecycle.SetEventEmitter(func(name string, payload any) {
		if name == agentEventName {
			agentEvents++
		}
	})

	bridge := NewEventBridge(nil, lifecycle, nil)
	bridge.publish("skills/changed", map[string]any{"name": "demo"})

	if agentEvents != 0 {
		t.Fatalf("agent event count = %d, want 0", agentEvents)
	}
}

func assertBridgeType(t *testing.T, payload map[string]any, want string) {
	t.Helper()

	if payload["type"] != want {
		t.Fatalf("payload type = %#v, want %q; payload=%#v", payload["type"], want, payload)
	}
}

func assertAgentEvent(t *testing.T, payload map[string]any, wantAgentID string, wantType string) {
	t.Helper()

	if payload["agent_id"] != wantAgentID {
		t.Fatalf("payload agent_id = %#v, want %q; payload=%#v", payload["agent_id"], wantAgentID, payload)
	}
	if payload["type"] != wantType {
		t.Fatalf("payload type = %#v, want %q; payload=%#v", payload["type"], wantType, payload)
	}
}
