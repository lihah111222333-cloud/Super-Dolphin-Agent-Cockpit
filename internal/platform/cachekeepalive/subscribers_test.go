package cachekeepalive

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewCacheKeepaliveSubscribersSpec(t *testing.T) {
	t.Parallel()

	manager := newTestManager(nil, nil, nil)
	t.Cleanup(manager.shutdownForTest)
	spec := NewCacheKeepaliveSubscribers(manager, nil).Spec

	if spec.EventType != "cache.keepalive.agent.launched" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "cachekeepalive.startKeepaliveRelay" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "cachekeepalive" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "cachekeepalive-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestCacheKeepaliveSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	manager := newTestManager(nil, &bindingStoreStub{byAgent: map[string]*contract.CacheKeepaliveBinding{"agent-1": {AgentID: "agent-1"}}}, nil)
	t.Cleanup(manager.shutdownForTest)
	spec := NewCacheKeepaliveSubscribers(manager, nil).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, agentdto.AgentLaunched{AgentSessionHeader: cacheKeepaliveAgentHeader("thread-1", "agent-1", "session-1")})
	waitForCacheKeepaliveTimer(t, manager, "session-1", true)

	cancel()
	cancel()

	event.Publish(dispatcher, agentdto.AgentLaunched{AgentSessionHeader: cacheKeepaliveAgentHeader("thread-2", "agent-1", "session-after-cancel")})
	time.Sleep(50 * time.Millisecond)
	waitForCacheKeepaliveTimer(t, manager, "session-after-cancel", false)
}

func TestCacheKeepaliveRelayClearsTimerOnAgentStopped(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	manager := newTestManager(nil, &bindingStoreStub{byAgent: map[string]*contract.CacheKeepaliveBinding{"agent-1": {AgentID: "agent-1"}}}, nil)
	t.Cleanup(manager.shutdownForTest)
	spec := NewCacheKeepaliveSubscribers(manager, nil).Spec
	cancel := spec.Register(dispatcher)
	t.Cleanup(cancel)

	event.Publish(dispatcher, agentdto.AgentLaunched{AgentSessionHeader: cacheKeepaliveAgentHeader("thread-1", "agent-1", "session-1")})
	waitForCacheKeepaliveTimer(t, manager, "session-1", true)

	event.Publish(dispatcher, agentdto.AgentStopped{AgentSessionHeader: cacheKeepaliveAgentHeader("thread-1", "agent-1", "session-1")})
	waitForCacheKeepaliveTimer(t, manager, "session-1", false)
}

func cacheKeepaliveAgentHeader(threadID, agentID, sessionID string) shareddto.AgentSessionHeader {
	return shareddto.AgentSessionHeader{
		AgentHeader: shareddto.AgentHeader{
			ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID},
			AgentID:      agentID,
		},
		SessionID: sessionID,
	}
}

func waitForCacheKeepaliveTimer(t *testing.T, manager *Manager, sessionUUID string, want bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		got := manager.snapshotTimer(sessionUUID, nil) != nil
		if got == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timer for %q presence = %v, want %v", sessionUUID, manager.snapshotTimer(sessionUUID, nil) != nil, want)
}
