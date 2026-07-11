package mcpcontrol

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewMCPConfigChangeSubscribersSpec(t *testing.T) {
	t.Parallel()

	worker := newConfigFanoutWorker(&fakeFanoutNotifier{}, &stubVersionSource{}, nil)
	spec := NewMCPConfigChangeSubscribers(worker, nil).Spec

	if spec.EventType != "mcpcontrol.config.change" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "mcpcontrol.registerConfigChangeSubscriptions" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "mcpcontrol" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "mcpcontrol-config-change-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestMCPConfigChangeSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	worker := newConfigFanoutWorker(&fakeFanoutNotifier{}, &stubVersionSource{}, nil)
	spec := NewMCPConfigChangeSubscribers(worker, nil).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, agentdto.StateChanged{AgentSessionHeader: mcpConfigAgentHeader("thread-1", "agent-1"), NewState: "running"})
	waitForMCPConfigEnqueued(t, worker, 1)

	cancel()
	cancel()

	event.Publish(dispatcher, agentdto.StateChanged{AgentSessionHeader: mcpConfigAgentHeader("thread-1", "agent-1"), NewState: "stopped"})
	time.Sleep(50 * time.Millisecond)
	if got := worker.EnqueuedTotal(); got != 1 {
		t.Fatalf("EnqueuedTotal after cancel = %d, want 1", got)
	}
}

func mcpConfigAgentHeader(threadID, agentID string) shareddto.AgentSessionHeader {
	return shareddto.AgentSessionHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID},
			AgentID:      agentID,
		},
	}
}

func waitForMCPConfigEnqueued(t *testing.T, worker *configFanoutWorker, want int64) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if worker.EnqueuedTotal() == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("EnqueuedTotal = %d, want %d", worker.EnqueuedTotal(), want)
}
