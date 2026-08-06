package memory

import (
	"context"
	"testing"
	"time"

	"github.com/kelindar/event"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	nestedpkg "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/nested"
)

// TestToolCallEndNestedIngestRejectionUpdatesHealth ensures bus admission
// rejection reaches the single production health state instead of logs alone.
func TestToolCallEndNestedIngestRejectionUpdatesHealth(t *testing.T) {
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	hooks := newMemoryLifecycleHooks(&Config{}, nil, nil, nil, nil, nil, nil, nil)
	nested := newNestedIngestWorker(&fakeNestedIngestRuntime{}, nil)
	if err := nested.Stop(context.Background()); err != nil {
		t.Fatalf("nested.Stop() error = %v", err)
	}

	var cancels []context.CancelFunc
	registerThreadHookSubscriptions(memorySubscriptionDeps{
		Dispatcher:    dispatcher,
		Hooks:         hooks,
		NestedRuntime: &nestedpkg.NestedRuntime{},
	}, nested, nil, nil, func(cancel context.CancelFunc) {
		cancels = append(cancels, cancel)
	})
	t.Cleanup(func() {
		for _, cancel := range cancels {
			cancel()
		}
	})

	event.Publish(dispatcher, tooldto.ToolCallEnd{ToolCallHeader: shared.ToolCallHeader{
		TurnHeader: shared.TurnHeader{AgentHeader: shared.AgentHeader{ThreadHeader: shared.ThreadHeader{ThreadID: "thread-1"}}},
		CallID:     "call-1",
		ToolName:   "read_file",
	}})
	var health NestedIngestHealthSnapshot
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		health = hooks.GetNestedIngestHealth()
		if health.RejectedTotal == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if health.RejectedTotal != 1 || health.LastThreadID != "thread-1" {
		t.Fatalf("nested ingest health = %+v, want rejected thread snapshot", health)
	}
	if health.LastError == "" || health.LastAt.IsZero() {
		t.Fatalf("nested ingest health = %+v, want error and timestamp", health)
	}
}
