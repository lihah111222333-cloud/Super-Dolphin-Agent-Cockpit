package uistate

import (
	"context"
	"testing"
	"time"

	"github.com/kelindar/event"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	platformbus "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/bus"
)

func TestNewUIStateSubscribersSpec(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService error = %v", err)
	}
	spec := NewUIStateSubscribers(svc).Spec

	if spec.EventType != "uistate.projections" {
		t.Fatalf("EventType = %q", spec.EventType)
	}
	if spec.HandlerSymbol != "uistate.registerProjectionSubscriptions" {
		t.Fatalf("HandlerSymbol = %q", spec.HandlerSymbol)
	}
	if spec.OwnerModule != "uistate" {
		t.Fatalf("OwnerModule = %q", spec.OwnerModule)
	}
	if spec.CancelOwner != "bus.SubscriberGroup" {
		t.Fatalf("CancelOwner = %q", spec.CancelOwner)
	}
	if spec.ShutdownClass != "bus-subscriber" {
		t.Fatalf("ShutdownClass = %q", spec.ShutdownClass)
	}
	if spec.TestFixtureID != "uistate-projections-subscribers" {
		t.Fatalf("TestFixtureID = %q", spec.TestFixtureID)
	}
	if spec.Register == nil {
		t.Fatal("Register must be non-nil")
	}
}

func TestUIStateSubscribersRegisterCancelAndDeliver(t *testing.T) {
	t.Parallel()

	dispatcher := platformbus.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService error = %v", err)
	}
	spec := NewUIStateSubscribers(svc).Spec

	cancel := spec.Register(dispatcher)
	if cancel == nil {
		t.Fatal("Register returned nil cancel")
	}

	event.Publish(dispatcher, uidto.UITokensUpdated{
		UITurnHeader:        uiStateSubscriberTurnHeader("thread-1", "turn-1"),
		InputTokens:         10,
		OutputTokens:        5,
		TotalTokens:         15,
		ContextWindowTokens: 100,
	})
	waitForUIStateTokenUsage(t, svc, "thread-1", 15)

	cancel()
	cancel()

	event.Publish(dispatcher, uidto.UITokensUpdated{
		UITurnHeader:        uiStateSubscriberTurnHeader("thread-1", "turn-2"),
		InputTokens:         20,
		OutputTokens:        5,
		TotalTokens:         25,
		ContextWindowTokens: 100,
	})
	time.Sleep(50 * time.Millisecond)
	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState error = %v", err)
	}
	if got := state.TokenUsages["thread-1"].TotalTokens; got != 15 {
		t.Fatalf("TotalTokens after cancel = %d, want 15", got)
	}
}

func uiStateSubscriberTurnHeader(threadID, turnID string) shareddto.UITurnHeader {
	return shareddto.UITurnHeader{
		UIProjectionHeader: shareddto.UIProjectionHeader{ThreadHeader: shareddto.ThreadHeader{ThreadID: threadID}},
		TurnIDHeader:       shareddto.TurnIDHeader{TurnID: turnID},
	}
}

func waitForUIStateTokenUsage(t *testing.T, svc *service, threadID string, want int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state, err := svc.GetState(context.Background())
		if err != nil {
			t.Fatalf("GetState error = %v", err)
		}
		if state.TokenUsages[threadID].TotalTokens == want {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState error = %v", err)
	}
	t.Fatalf("TotalTokens = %d, want %d", state.TokenUsages[threadID].TotalTokens, want)
}
