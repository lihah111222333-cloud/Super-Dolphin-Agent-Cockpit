package uistate

import (
	"context"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

func TestGetSidebarBuildsCompatibilitySnapshot(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", Name: "Demo", AgentID: "agent-1"}}
	svc.state.Agents = []AgentSummary{{
		ID:          "agent-1",
		Name:        "Demo",
		ThreadID:    "thread-1",
		State:       "running",
		AgentState:  "running",
		Provider:    "claude",
		CWD:         "/tmp/demo",
		Port:        8080,
		LastReport:  "final answer",
		LastMessage: "final answer",
	}}

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if got := sidebar.Statuses["thread-1"]; got != "running" {
		t.Fatalf("sidebar.Statuses[thread-1] = %q, want running", got)
	}
	if got := sidebar.StatusHeadersByThread["thread-1"]; got != "工作中" {
		t.Fatalf("sidebar.StatusHeadersByThread[thread-1] = %q, want 工作中", got)
	}
	if !sidebar.InterruptibleByThread["thread-1"] {
		t.Fatal("sidebar.InterruptibleByThread[thread-1] = false, want true")
	}
	if got := sidebar.Threads[0].ThreadStatus; got != "running" {
		t.Fatalf("thread.threadStatus = %q, want running", got)
	}
	if got := sidebar.Threads[0].AgentState; got != "running" {
		t.Fatalf("thread.agentState = %q, want running", got)
	}
	if got := sidebar.Threads[0].LastMessage; got != "final answer" {
		t.Fatalf("thread.lastMessage = %q, want final answer", got)
	}
	runtime := sidebar.AgentRuntimeByID["thread-1"]
	if got, _ := runtime["provider"].(string); got != "claude" {
		t.Fatalf("agentRuntimeById[thread-1].provider = %q, want claude", got)
	}
	if got, _ := runtime["providerThreadId"].(string); got != "thread-1" {
		t.Fatalf("agentRuntimeById[thread-1].providerThreadId = %q, want thread-1", got)
	}
	if got, _ := runtime["cwd"].(string); got != "/tmp/demo" {
		t.Fatalf("agentRuntimeById[thread-1].cwd = %q, want /tmp/demo", got)
	}
}

func TestApplyThreadUpdatedSyncsSidebarModel(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"},
		SessionID:   "session-1",
	}
	launchedModel := "gpt-5.4"
	svc.applyAgentLaunched(agentdto.AgentLaunched{AgentSessionHeader: header, Model: launchedModel, CWD: "/tmp/demo"})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after launch error = %v", err)
	}
	if got, _ := sidebar.AgentRuntimeByID["thread-1"]["model"].(string); got != launchedModel {
		t.Fatalf("launch model = %q, want %q", got, launchedModel)
	}

	updatedModel := "gpt-5.5"
	svc.applyThreadUpdated(threaddto.Updated{ThreadID: "thread-1", Model: &updatedModel})
	sidebar, err = svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after update error = %v", err)
	}
	if got, _ := sidebar.AgentRuntimeByID["thread-1"]["model"].(string); got != updatedModel {
		t.Fatalf("updated model = %q, want %q", got, updatedModel)
	}

	clearedModel := ""
	svc.applyThreadUpdated(threaddto.Updated{ThreadID: "thread-1", Model: &clearedModel})
	sidebar, err = svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after clear error = %v", err)
	}
	if _, ok := sidebar.AgentRuntimeByID["thread-1"]["model"]; ok {
		t.Fatalf("cleared runtime = %#v, want model removed", sidebar.AgentRuntimeByID["thread-1"])
	}
}

func TestSetPreferencePublishesProjectionUpdatesForSettingsKeys(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)

	prefChanged := make(chan uidto.UIPreferencesChanged, 1)
	cancelPref := event.Subscribe(dispatcher, func(ev uidto.UIPreferencesChanged) { prefChanged <- ev })
	defer cancelPref()

	projectionChanged := make(chan uidto.UIProjectionUpdated, 2)
	cancelProjection := event.Subscribe(dispatcher, func(ev uidto.UIProjectionUpdated) { projectionChanged <- ev })
	defer cancelProjection()

	if err := svc.SetPreference(context.Background(), "settings.provider.active", "claude"); err != nil {
		t.Fatalf("SetPreference(settings.provider.active) error = %v", err)
	}

	prefEvent := mustReceivePreferenceChanged(t, prefChanged)
	if prefEvent.Key != "settings.provider.active" {
		t.Fatalf("prefEvent.Key = %q, want settings.provider.active", prefEvent.Key)
	}

	first := mustReceiveProjectionUpdated(t, projectionChanged)
	second := mustReceiveProjectionUpdated(t, projectionChanged)
	got := map[string]int64{
		first.Projection:  first.Revision,
		second.Projection: second.Revision,
	}
	if got["state"] != 1 || got["sidebar"] != 1 {
		t.Fatalf("projection revisions = %#v, want state=1 sidebar=1", got)
	}
}

func TestProjectionSubscriptionsUpdateSidebarFromLifecycleAndOutputEvents(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)
	cancels := registerProjectionSubscriptions(dispatcher, svc)
	defer func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
	}()

	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{
				EventHeader: sharedto.EventHeader{Timestamp: time.Now()},
				ThreadID:    "thread-1",
			},
			AgentID: "agent-1",
		},
		SessionID: "session-1",
	}

	event.Publish(dispatcher, agentdto.AgentLaunched{
		AgentSessionHeader: header,
		CWD:                "/tmp/demo",
	})
	event.Publish(dispatcher, agentdto.AgentRuntimeReported{
		AgentSessionHeader: header,
		Port:               8080,
		Provider:           "claude",
	})
	event.Publish(dispatcher, agentdto.StateChanged{
		AgentSessionHeader: header,
		NewState:           "running",
	})
	event.Publish(dispatcher, turndto.TurnStarted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: header.AgentHeader,
			TurnIDHeader: sharedto.TurnIDHeader{
				TurnID: "turn-1",
			},
		},
	})
	event.Publish(dispatcher, turndto.TurnOutputDelta{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: header.AgentHeader,
			TurnIDHeader: sharedto.TurnIDHeader{
				TurnID: "turn-1",
			},
		},
		Stream: "message",
		Delta:  "hello world",
	})
	event.Publish(dispatcher, turndto.TurnInterrupted{
		TurnHeader: sharedto.TurnHeader{
			AgentHeader: header.AgentHeader,
			TurnIDHeader: sharedto.TurnIDHeader{
				TurnID: "turn-1",
			},
		},
		Reason: "user_requested",
	})
	var sidebar *Sidebar
	deadline := time.Now().Add(time.Second)
	for {
		sidebar, err = svc.GetSidebar(context.Background())
		if err != nil {
			t.Fatalf("GetSidebar() error = %v", err)
		}
		if sidebar.ActiveTurn == nil && sidebar.Statuses["thread-1"] == "idle" {
			break
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if sidebar.ActiveTurn != nil {
		t.Fatalf("sidebar.ActiveTurn = %#v, want nil after interrupt", sidebar.ActiveTurn)
	}
	if got := sidebar.Threads[0].LastMessage; got != "hello world" {
		t.Fatalf("thread.lastMessage = %q, want hello world", got)
	}
	if got := sidebar.Threads[0].AgentState; got != "running" {
		t.Fatalf("thread.agentState = %q, want running", got)
	}
	if got := sidebar.Statuses["thread-1"]; got != "idle" {
		t.Fatalf("sidebar.Statuses[thread-1] = %q, want idle", got)
	}
	runtime := sidebar.AgentRuntimeByID["thread-1"]
	if got, _ := runtime["provider"].(string); got != "claude" {
		t.Fatalf("agentRuntimeById[thread-1].provider = %q, want claude", got)
	}
	if got, _ := runtime["state"].(string); got != "running" {
		t.Fatalf("agentRuntimeById[thread-1].state = %q, want running", got)
	}
}

func mustReceivePreferenceChanged(t *testing.T, ch <-chan uidto.UIPreferencesChanged) uidto.UIPreferencesChanged {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("expected UIPreferencesChanged event")
		return uidto.UIPreferencesChanged{}
	}
}

func mustReceiveProjectionUpdated(t *testing.T, ch <-chan uidto.UIProjectionUpdated) uidto.UIProjectionUpdated {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("expected UIProjectionUpdated event")
		return uidto.UIProjectionUpdated{}
	}
}
