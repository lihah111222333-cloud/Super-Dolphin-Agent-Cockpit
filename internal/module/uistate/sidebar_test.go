package uistate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

func TestGetSidebarBuildsCompatibilitySnapshot(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
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
	svc.state.ActiveTurn = &TurnSummary{ID: "turn-1", ThreadID: "thread-1", Status: "running"}

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	assertCompatibilitySidebarSnapshot(t, sidebar)
}

func TestSortThreadsUsesNewestCreatedAtFirst(t *testing.T) {
	oldCreatedAt := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	newCreatedAt := oldCreatedAt.Add(time.Minute)
	items := []ThreadSummary{
		{ID: "thread-old", Name: "Z old", CreatedAt: &oldCreatedAt},
		{ID: "thread-new", Name: "A new", CreatedAt: &newCreatedAt},
	}

	sortThreads(items)

	if got := []string{items[0].ID, items[1].ID}; !reflect.DeepEqual(got, []string{"thread-new", "thread-old"}) {
		t.Fatalf("sortThreads() order = %#v, want newest created first", got)
	}
}

func TestSortThreadsFallsBackToUpdatedAtWhenCreatedAtMissing(t *testing.T) {
	oldUpdatedAt := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	newUpdatedAt := oldUpdatedAt.Add(time.Minute)
	items := []ThreadSummary{
		{ID: "thread-old", Name: "A old", UpdatedAt: &oldUpdatedAt},
		{ID: "thread-new", Name: "Z new", UpdatedAt: &newUpdatedAt},
	}

	sortThreads(items)

	if got := []string{items[0].ID, items[1].ID}; !reflect.DeepEqual(got, []string{"thread-new", "thread-old"}) {
		t.Fatalf("sortThreads() order = %#v, want newest updated first", got)
	}
}

func TestBuildInitialStateSortsThreadsByThreadRefCreatedAt(t *testing.T) {
	t.Parallel()

	oldCreatedAt := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	newCreatedAt := oldCreatedAt.Add(time.Minute)
	state, err := buildInitialState(context.Background(), &threadListerStub{refs: []contract.ThreadRef{
		{ID: "thread-old", Name: "A old", CreatedAt: oldCreatedAt.UnixMilli()},
		{ID: "thread-new", Name: "Z new", CreatedAt: newCreatedAt.UnixMilli()},
	}}, nil)
	if err != nil {
		t.Fatalf("buildInitialState() error = %v", err)
	}
	if got := []string{state.Threads[0].ID, state.Threads[1].ID}; !reflect.DeepEqual(got, []string{"thread-new", "thread-old"}) {
		t.Fatalf("buildInitialState() thread order = %#v, want newest created_at first", got)
	}
	if state.Threads[0].CreatedAt == nil || !state.Threads[0].CreatedAt.Equal(newCreatedAt) {
		t.Fatalf("thread-new CreatedAt = %v, want %s", state.Threads[0].CreatedAt, newCreatedAt)
	}
	if state.Threads[0].UpdatedAt != nil {
		t.Fatalf("thread-new UpdatedAt = %v, want nil for zero source updated_at", state.Threads[0].UpdatedAt)
	}
}

func TestBuildInitialStateReadsAgentsThroughListAgentsOnly(t *testing.T) {
	t.Parallel()

	createdAt := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	lister := &agentListStub{items: []contract.AgentSnapshot{{
		ID:        "agent-1",
		Name:      "Agent One",
		ThreadID:  "thread-1",
		State:     "running",
		Provider:  "codex",
		Cwd:       "/tmp/demo",
		CreatedAt: createdAt,
		UpdatedAt: createdAt.Add(time.Minute),
	}}}

	state, err := buildInitialState(context.Background(), nil, lister)
	if err != nil {
		t.Fatalf("buildInitialState() error = %v", err)
	}
	if lister.calls != 1 {
		t.Fatalf("ListAgents() calls = %d, want 1", lister.calls)
	}
	if len(state.Agents) != 1 || state.Agents[0].ID != "agent-1" || state.Agents[0].ThreadID != "thread-1" {
		t.Fatalf("Agents = %#v, want summarized agent from ListAgents", state.Agents)
	}
	if len(state.Threads) != 1 || state.Threads[0].ID != "thread-1" || state.Threads[0].AgentID != "agent-1" {
		t.Fatalf("Threads = %#v, want thread derived from agent snapshot", state.Threads)
	}
}

func TestBuildInitialStateReturnsListAgentsError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("agent list unavailable")
	_, err := buildInitialState(context.Background(), nil, &agentListStub{err: wantErr})
	if !errors.Is(err, wantErr) {
		t.Fatalf("buildInitialState() error = %v, want %v", err, wantErr)
	}
}

func TestSortAgentsUsesNewestCreatedAtFirst(t *testing.T) {
	oldCreatedAt := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	newCreatedAt := oldCreatedAt.Add(time.Minute)
	items := []AgentSummary{
		{ID: "agent-old", Name: "Z old", CreatedAt: &oldCreatedAt},
		{ID: "agent-new", Name: "A new", CreatedAt: &newCreatedAt},
	}

	sortAgents(items)

	if got := []string{items[0].ID, items[1].ID}; !reflect.DeepEqual(got, []string{"agent-new", "agent-old"}) {
		t.Fatalf("sortAgents() order = %#v, want newest created first", got)
	}
}

func TestSummarizeThreadsCarriesCreatedAtForSorting(t *testing.T) {
	items := summarizeThreads([]contract.ThreadRef{
		{ID: "thread-old", Name: "Z old", CreatedAt: 1710000000000, UpdatedAt: 1710000000000},
		{ID: "thread-new", Name: "A new", CreatedAt: 1710000060000, UpdatedAt: 1710000060000},
	})

	sortThreads(items)

	if got := []string{items[0].ID, items[1].ID}; !reflect.DeepEqual(got, []string{"thread-new", "thread-old"}) {
		t.Fatalf("summarized thread order = %#v, want newest created first", got)
	}
	if items[0].CreatedAt == nil || items[0].CreatedAt.Year() != 2024 {
		t.Fatalf("summarized CreatedAt = %v, want normalized 2024 timestamp", items[0].CreatedAt)
	}
}

func assertCompatibilitySidebarSnapshot(t *testing.T, sidebar *Sidebar) {
	t.Helper()
	assertCompatibilitySidebarStatus(t, sidebar)
	assertCompatibilitySidebarThread(t, sidebar)
	assertCompatibilitySidebarRuntime(t, sidebar)
}

func assertCompatibilitySidebarStatus(t *testing.T, sidebar *Sidebar) {
	t.Helper()
	if got := sidebar.Statuses["thread-1"]; got != "running" {
		t.Fatalf("sidebar.Statuses[thread-1] = %q, want running", got)
	}
	if sidebar.ActiveTurn == nil || sidebar.ActiveTurn.ID != "turn-1" || sidebar.ActiveTurn.ThreadID != "thread-1" {
		t.Fatalf("sidebar.ActiveTurn = %#v, want active turn identity for sidebar snapshot interrupt gate", sidebar.ActiveTurn)
	}
	if got := sidebar.StatusHeadersByThread["thread-1"]; got != "工作中" {
		t.Fatalf("sidebar.StatusHeadersByThread[thread-1] = %q, want 工作中", got)
	}
	if !sidebar.InterruptibleByThread["thread-1"] {
		t.Fatal("sidebar snapshot InterruptibleByThread[thread-1] = false, want true for matching active turn")
	}
}

func assertCompatibilitySidebarThread(t *testing.T, sidebar *Sidebar) {
	t.Helper()
	if got := sidebar.Threads[0].ThreadStatus; got != "running" {
		t.Fatalf("thread.threadStatus = %q, want running", got)
	}
	if got := sidebar.Threads[0].AgentState; got != "running" {
		t.Fatalf("thread.agentState = %q, want running", got)
	}
	if got := sidebar.Threads[0].LastMessage; got != "final answer" {
		t.Fatalf("thread.lastMessage = %q, want final answer", got)
	}
}

func assertCompatibilitySidebarRuntime(t *testing.T, sidebar *Sidebar) {
	t.Helper()
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

func TestApplyBindingToThreadRuntimeBackfillsProviderIdentity(t *testing.T) {
	t.Parallel()

	const providerUUID = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	rolloutPath := writeExistingProviderHistoryFile(t)
	runtimeMap := map[string]map[string]any{
		"thread-1": {
			"agentId":          "agent-1",
			"state":            "syncing",
			"providerThreadId": "agent_1778679524655355000",
		},
	}
	applyBindingToThreadRuntime(
		ThreadSummary{ID: "thread-1", AgentID: "agent-1"},
		map[string]BindingEntry{
			"agent-1": {
				AgentID:          "agent-1",
				Provider:         "codex",
				ProviderThreadID: "agent_1778679524655355000",
				SessionUUID:      providerUUID,
				RolloutPath:      rolloutPath,
				Cwd:              "/repo",
			},
		},
		runtimeMap,
	)

	runtime := runtimeMap["thread-1"]
	if got, _ := runtime["provider"].(string); got != "codex" {
		t.Fatalf("runtime.provider = %q, want codex", got)
	}
	if got, _ := runtime["providerThreadId"].(string); got != providerUUID {
		t.Fatalf("runtime.providerThreadId = %q, want %s", got, providerUUID)
	}
	if got, _ := runtime["cwd"].(string); got != "/repo" {
		t.Fatalf("runtime.cwd = %q, want /repo", got)
	}
	if got, _ := runtime["state"].(string); got != "syncing" {
		t.Fatalf("runtime.state = %q, want syncing", got)
	}
}

func TestApplyBindingToThreadRuntimeDoesNotBackfillProviderIdentityWithoutHistoryFile(t *testing.T) {
	t.Parallel()

	const providerUUID = "019e218f-b514-7733-be85-b3ee7f6a78a6"
	runtimeMap := map[string]map[string]any{
		"thread-1": {
			"agentId":          "agent-1",
			"state":            "syncing",
			"providerThreadId": "agent_1778679524655355000",
		},
	}
	applyBindingToThreadRuntime(
		ThreadSummary{ID: "thread-1", AgentID: "agent-1"},
		map[string]BindingEntry{
			"agent-1": {
				AgentID:          "agent-1",
				Provider:         "codex",
				ProviderThreadID: "agent_1778679524655355000",
				SessionUUID:      providerUUID,
				Cwd:              "/repo",
			},
		},
		runtimeMap,
	)

	runtime := runtimeMap["thread-1"]
	if got, _ := runtime["providerThreadId"].(string); got != "agent_1778679524655355000" {
		t.Fatalf("runtime.providerThreadId = %q, want placeholder retained", got)
	}
}

func writeExistingProviderHistoryFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history.jsonl")
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write provider history file: %v", err)
	}
	return path
}

func TestGetSidebarDoesNotExposeLegacyTaskMetadataInRuntime(t *testing.T) {
	t.Parallel()

	threads := &configThreadServiceStub{
		runtimeConfigResult: map[string]any{
			"taskId":      "task-demo",
			"taskTitle":   "Memory Center Refactor",
			"handoffFile": "legacy-task.md",
		},
	}
	svc, _, err := NewService(nil, nil, nil, nil, nil, threads)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", Name: "Demo", AgentID: "agent-1"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-1", ThreadID: "thread-1", State: "running", AgentState: "running"}}

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	runtime := sidebar.AgentRuntimeByID["thread-1"]
	for _, key := range []string{"taskId", "taskTitle", "handoffFile"} {
		if _, ok := runtime[key]; ok {
			t.Fatalf("runtime[%q] unexpectedly exposed legacy task metadata: %+v", key, runtime)
		}
	}
}

func TestApplyThreadUpdatedSyncsSidebarModel(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"}, AgentID: "agent-1"},
		SessionID:   "session-1",
	}
	launchedModel := "gpt-5.5"
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

func TestThreadStartedProjectsAuthoritativeAgentBoardFields(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	now := time.Now().UTC()
	currentStep := "执行实现"
	board := &agentdto.BoardView{
		ID: "agent-1", ThreadID: "thread-1", ParentAgentID: "parent-1", Name: "worker",
		Assignment: &agentdto.Assignment{Title: "修复 bootstrap", Description: "保持 Agent 看板权威字段", AssignedAt: now},
		Progress:   agentdto.Progress{Status: "turn_running", CurrentStep: &currentStep, UpdatedAt: now},
	}
	svc.applyThreadStarted(threaddto.Started{
		EventHeader: sharedto.EventHeader{Timestamp: now.Add(time.Second)},
		ThreadID:    "thread-1", AgentID: "agent-1", Provider: "codex", ProviderThreadID: "provider-thread-1", Name: "worker",
		Board: board,
	})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if len(sidebar.Agents) != 1 {
		t.Fatalf("agents = %#v, want one agent", sidebar.Agents)
	}
	got := sidebar.Agents[0]
	if got.ParentAgentID != "parent-1" || got.Assignment == nil || got.Assignment.Title != "修复 bootstrap" {
		t.Fatalf("board identity/assignment overwritten by thread.Started: %#v", got)
	}
	if err := got.Progress.Validate(); err != nil {
		t.Fatalf("progress after thread.Started is invalid: %v (%#v)", err, got.Progress)
	}
	if got.Progress.Status != "turn_running" || !got.Progress.UpdatedAt.Equal(now) {
		t.Fatalf("progress overwritten by thread.Started: %#v", got.Progress)
	}
}

func TestSetPreferencePublishesProjectionUpdatesForSettingsKeys(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
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

	dispatcher, svc := newSidebarProjectionTestService(t)
	header := sidebarProjectionHeader()
	publishSidebarLifecycleEvents(dispatcher, header)
	waitForSidebarState(t, svc, func(sidebar *Sidebar) bool {
		return sidebar.ActiveTurn != nil
	})
	publishSidebarTurnOutput(dispatcher, header)
	waitForSidebarState(t, svc, func(sidebar *Sidebar) bool {
		return len(sidebar.Threads) > 0 && sidebar.Threads[0].LastMessage == "hello world"
	})
	publishSidebarTurnInterrupted(dispatcher, header)
	sidebar := waitForSidebarState(t, svc, func(sidebar *Sidebar) bool {
		return sidebar.ActiveTurn == nil &&
			sidebar.Statuses["thread-1"] == "idle" &&
			len(sidebar.Threads) > 0 &&
			sidebar.Threads[0].LastMessage == "hello world"
	})
	assertInterruptedSidebarProjection(t, sidebar)
}

func newSidebarProjectionTestService(t *testing.T) (*event.Dispatcher, *service) {
	t.Helper()
	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.bindDispatcher(dispatcher)
	cancels := registerProjectionSubscriptions(dispatcher, svc)
	t.Cleanup(func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
	})
	return dispatcher, svc
}

func sidebarProjectionHeader() sharedto.AgentSessionHeader {
	return sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{
				EventHeader: sharedto.EventHeader{Timestamp: time.Now()},
				ThreadID:    "thread-1",
			},
			AgentID: "agent-1",
		},
		SessionID: "session-1",
	}
}

func publishSidebarLifecycleEvents(dispatcher *event.Dispatcher, header sharedto.AgentSessionHeader) {
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
	event.Publish(dispatcher, turndto.TurnStarted{TurnHeader: sidebarTurnHeader(header)})
}

func publishSidebarTurnOutput(dispatcher *event.Dispatcher, header sharedto.AgentSessionHeader) {
	event.Publish(dispatcher, turndto.TurnOutputDelta{
		TurnHeader: sidebarTurnHeader(header),
		Stream:     "message",
		Delta:      "hello world",
	})
}

func publishSidebarTurnInterrupted(dispatcher *event.Dispatcher, header sharedto.AgentSessionHeader) {
	event.Publish(dispatcher, turndto.TurnInterrupted{
		TurnHeader: sidebarTurnHeader(header),
		Reason:     "user_requested",
	})
}

func sidebarTurnHeader(header sharedto.AgentSessionHeader) sharedto.TurnHeader {
	return sharedto.TurnHeader{
		AgentHeader:  header.AgentHeader,
		TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
	}
}

func assertInterruptedSidebarProjection(t *testing.T, sidebar *Sidebar) {
	t.Helper()
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

func waitForSidebarState(t *testing.T, svc *service, match func(*Sidebar) bool) *Sidebar {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		sidebar, err := svc.GetSidebar(context.Background())
		if err != nil {
			t.Fatalf("GetSidebar() error = %v", err)
		}
		if match(sidebar) {
			return sidebar
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("sidebar did not reach expected state")
	return nil
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

func TestSummarizeThreadsProjectsArchivedStatus(t *testing.T) {
	t.Parallel()

	got := summarizeThreads([]contract.ThreadRef{{ID: "thread-archived", Name: "Archived", AgentID: "agent-1", Status: " archived "}})
	if len(got) != 1 || got[0].State != "archived" || got[0].LifecycleStatus != "archived" {
		t.Fatalf("summarizeThreads() = %#v, want state/lifecycle archived", got)
	}
}

func TestGetSidebarProjectsDBArchivedStatusIntoArchivesMap(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.state.Threads = []ThreadSummary{{ID: "thread-archived", Name: "Archived", State: "archived"}}

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if got := sidebar.ThreadArchivesChat["thread-archived"]; got <= 0 {
		t.Fatalf("ThreadArchivesChat[thread-archived] = %d, want > 0", got)
	}
}

// HEAD~1 regression rollback: union-only projection retains preference
// when ThreadSummary.State is non-archived. ThreadSummary.State is a
// union field (DB lifecycle vs runtime), so we cannot safely treat
// runtime State!="archived" as a signal to drop preference timestamps.
// A future ThreadSummary.LifecycleStatus split will let unarchive drop
// stale preference safely.

func TestApplyThreadStoppedDeletedRemovesSidebarThread(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	svc.state.Threads = []ThreadSummary{{
		ID:              "thread-empty",
		Name:            "thread-empty",
		AgentID:         "agent-empty",
		LifecycleStatus: "archived",
		State:           "archived",
		ThreadStatus:    "archived",
	}}
	svc.state.Agents = []AgentSummary{{ID: "agent-empty", ThreadID: "thread-empty", State: "stopped"}}
	svc.applyThreadStopped(threaddto.Stopped{ThreadID: "thread-empty", AgentID: "agent-empty", Status: "deleted", Reason: "deleted_pending_launch"})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	for _, thread := range sidebar.Threads {
		if thread.ID == "thread-empty" {
			t.Fatalf("deleted thread still present in sidebar: %#v", thread)
		}
	}
}

func TestApplyThreadStoppedUnarchiveClearsLifecycleArchive(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.state.Threads = []ThreadSummary{{
		ID:              "thread-1",
		AgentID:         "agent-1",
		LifecycleStatus: "archived",
		State:           "archived",
	}}
	svc.applyThreadStopped(threaddto.Stopped{ThreadID: "thread-1", Status: "created", Reason: "unarchived"})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if got := sidebar.ThreadArchivesChat["thread-1"]; got != 0 {
		t.Fatalf("ThreadArchivesChat[thread-1] = %d, want 0 after unarchive", got)
	}
	if got := sidebar.Threads[0].AgentID; got != "agent-1" {
		t.Fatalf("AgentID = %q, want preserved agent-1", got)
	}
}

func TestGetSidebarProjectsLifecycleArchivedAfterRuntimeStateDerivation(t *testing.T) {
	t.Parallel()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	svc.state.Threads = []ThreadSummary{{
		ID:              "thread-archived",
		Name:            "Archived",
		LifecycleStatus: "archived",
		State:           "idle",
		ThreadStatus:    "idle",
	}}

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if got := sidebar.ThreadArchivesChat["thread-archived"]; got <= 0 {
		t.Fatalf("ThreadArchivesChat[thread-archived] = %d, want > 0 from lifecycle archived", got)
	}
}

func TestProjectArchivedThreadStatusKeepsPreferenceWhenStateIsNotArchived(t *testing.T) {
	threads := []ThreadSummary{{ID: "t1", State: "created"}}
	archived := map[string]int64{"t1": 12345}
	got := projectArchivedThreadStatus(threads, archived)
	if got["t1"] != 12345 {
		t.Fatalf("ThreadArchivesChat[t1] = %d, want 12345 preserved (union-only projection until LifecycleStatus is split)", got["t1"])
	}
}

func TestProjectArchivedThreadStatusKeepsPreferenceWhenStateAbsent(t *testing.T) {
	// No corresponding ThreadSummary entry → preference fallback preserved.
	threads := []ThreadSummary{}
	archived := map[string]int64{"t1": 99}
	got := projectArchivedThreadStatus(threads, archived)
	if got["t1"] != 99 {
		t.Fatalf("ThreadArchivesChat[t1] = %d, want 99 (preference fallback when DB state absent)", got["t1"])
	}
}

func TestProjectArchivedThreadStatusForcesArchivedWhenDBSaysArchived(t *testing.T) {
	threads := []ThreadSummary{{ID: "t1", State: "archived"}}
	archived := map[string]int64{}
	got := projectArchivedThreadStatus(threads, archived)
	if got["t1"] < 1 {
		t.Fatalf("ThreadArchivesChat[t1] = %d, want >= 1 (DB archived must force entry)", got["t1"])
	}
}

func TestProjectArchivedThreadStatusUsesThreadUpdatedAtForDBArchiveTime(t *testing.T) {
	updatedAt := time.Date(2026, 5, 22, 10, 0, 0, 0, time.UTC)
	createdAt := updatedAt.Add(-time.Hour)
	threads := []ThreadSummary{{
		ID:              "t1",
		LifecycleStatus: "archived",
		CreatedAt:       &createdAt,
		UpdatedAt:       &updatedAt,
	}}

	got := projectArchivedThreadStatus(threads, map[string]int64{})

	if got["t1"] != updatedAt.UnixMilli() {
		t.Fatalf("ThreadArchivesChat[t1] = %d, want updated_at %d", got["t1"], updatedAt.UnixMilli())
	}
}
