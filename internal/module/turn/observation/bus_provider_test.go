package observation

import (
	"testing"
	"time"

	"github.com/kelindar/event"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewObservationSubscribersSpec(t *testing.T) {
	t.Parallel()

	result := NewObservationSubscribers(NewMemory(), nil)
	spec := result.Spec

	if spec.EventType != "turn.observation" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "observation.Subscribe" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "observation" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "observation-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestObservationSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	mem := NewMemory()
	spec := NewObservationSubscribers(mem, nil).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	startedAt := time.Now().UTC()
	event.Publish(dispatcher, turndto.TurnStarted{
		TurnHeader: observationBusTestTurnHeader("thread-1", "turn-1", startedAt),
	})
	waitFor(t, func() bool {
		ts, ok := mem.Timestamps("turn-1")
		return ok && ts.StartedAt.Equal(startedAt)
	}, "turn started event to reach observation memory")

	cancel()
	cancel()

	afterCancelAt := startedAt.Add(time.Second)
	event.Publish(dispatcher, turndto.TurnStarted{
		TurnHeader: observationBusTestTurnHeader("thread-1", "turn-after-cancel", afterCancelAt),
	})
	time.Sleep(50 * time.Millisecond)
	if _, ok := mem.Timestamps("turn-after-cancel"); ok {
		t.Fatal("turn-after-cancel was recorded after cancel")
	}
}

func observationBusTestTurnHeader(threadID, turnID string, at time.Time) sharedto.TurnHeader {
	return sharedto.TurnHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{
				EventHeader: sharedto.EventHeader{Timestamp: at},
				ThreadID:    threadID,
			},
			AgentID: "agent-1",
		},
		TurnIDHeader: sharedto.TurnIDHeader{TurnID: turnID},
	}
}
