package uistate

import (
	"context"
	"testing"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

func TestGetStatePrefersOverlayUntilTTLExpires(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", State: "idle", ThreadStatus: "idle"}}

	svc.mu.Lock()
	svc.setThreadOverlayLocked("thread-1", overlayTypeMCPStartup, "", overlayPriorityMCPStartup, 5*time.Millisecond)
	svc.mu.Unlock()

	state := mustGetUIState(t, svc, context.Background(), "GetState()")
	thread := mustThread(t, state.Threads, "thread-1")
	assertOverlayThreadStatus(t, thread, "starting", "starting", "thread status")
	assertThreadOverlay(t, thread, overlayTypeMCPStartup, "MCP 启动中", overlayPriorityMCPStartup, "thread overlay")

	time.Sleep(20 * time.Millisecond)

	state = mustGetUIState(t, svc, context.Background(), "GetState() after TTL")
	thread = mustThread(t, state.Threads, "thread-1")
	assertOverlayThreadStatus(t, thread, "idle", "idle", "thread status after TTL")
	assertThreadOverlay(t, thread, "", "", 0, "thread overlay after TTL")
}

func TestGetSidebarKeepsHigherPriorityOverlay(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", State: "idle", ThreadStatus: "idle"}}

	svc.mu.Lock()
	svc.setThreadOverlayLocked("thread-1", overlayTypeTerminalWait, "等待终端输入", 90, time.Minute)
	svc.setThreadOverlayLocked("thread-1", overlayTypeMCPStartup, "", overlayPriorityMCPStartup, time.Minute)
	svc.mu.Unlock()

	sidebar := mustGetSidebar(t, svc, "GetSidebar()")
	thread := mustThread(t, sidebar.Threads, "thread-1")
	assertThreadOverlay(t, thread, overlayTypeTerminalWait, "等待终端输入", 90, "thread overlay")
	assertOverlayThreadStatus(t, thread, "waiting", "waiting", "thread status")
	assertSidebarStatusText(t, sidebar, "thread-1", "waiting", "等待终端输入", "命令正在等待终端输入")
}

func TestAgentRuntimeReportedClearsStartupOverlay(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"},
			AgentID:      "agent-1",
		},
		SessionID: "session-1",
	}

	svc.applyAgentLaunched(agentdto.AgentLaunched{AgentSessionHeader: header})

	before := mustGetSidebar(t, svc, "GetSidebar() before ready")
	if got := before.StatusHeadersByThread["thread-1"]; got != "MCP 启动中" {
		t.Fatalf("startup header = %q, want MCP 启动中", got)
	}

	svc.applyAgentRuntimeReported(agentdto.AgentRuntimeReported{
		AgentSessionHeader: header,
		Provider:           "claude",
		Port:               8080,
	})
	svc.applyAgentStateChanged(agentdto.StateChanged{
		AgentSessionHeader: header,
		NewState:           "idle",
	})

	after := mustGetSidebar(t, svc, "GetSidebar() after ready")
	thread := mustThread(t, after.Threads, "thread-1")
	assertThreadOverlay(t, thread, "", "", 0, "thread overlay after ready")
	assertStringMapValue(t, after.Statuses, "thread-1", "idle", "sidebar.Statuses[thread-1]")
	assertStringMapValue(t, after.StatusHeadersByThread, "thread-1", "等待指示", "sidebar.StatusHeadersByThread[thread-1]")
}

func TestGetStateIncludesRuntimeSnapshotContractFields(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	startedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", Name: "主线程", AgentID: "agent-main", State: "running", ThreadStatus: "running", AgentState: "running", LastMessage: "正在处理"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-main", ThreadID: "thread-1", Provider: "claude", ProviderThreadID: "provider-1", CWD: "/repo", State: "running", AgentState: "running"}}
	svc.state.ActiveTurn = &TurnSummary{ID: "turn-1", AgentID: "agent-main", ThreadID: "thread-1", Status: "running", StartedAt: &startedAt}
	svc.state.RecentTurns = []TurnSummary{{ID: "turn-1", AgentID: "agent-main", ThreadID: "thread-1", Status: "running", StartedAt: &startedAt}}
	svc.state.TokenUsages = map[string]TokenUsage{"thread-1": {TotalTokens: 53, ContextWindowTokens: 200}}
	svc.state.MainAgentID = "agent-main"

	state := mustGetUIState(t, svc, withDiffStateRequest(context.Background(), "thread-1", false, 0), "GetState()")
	assertRuntimeSnapshotStatus(t, state)
	assertRuntimeSnapshotMaps(t, state, startedAt)
}

func TestAgentLifecyclePublishesOverlayThreadPatch(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc := mustNewUIStateService(t)
	svc.bindDispatcher(dispatcher)

	patches := make(chan uidto.UIThreadPatch, 4)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { patches <- ev })
	defer cancel()

	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"},
			AgentID:      "agent-1",
		},
		SessionID: "session-1",
	}

	svc.applyAgentLaunched(agentdto.AgentLaunched{AgentSessionHeader: header})
	launchPatch := mustReceiveThreadPatch(t, patches)
	assertLaunchOverlayPatch(t, launchPatch)

	svc.applyAgentRuntimeReported(agentdto.AgentRuntimeReported{
		AgentSessionHeader: header,
		Provider:           "claude",
		Port:               8080,
	})
	clearPatch := mustReceiveThreadPatch(t, patches)
	assertClearOverlayPatch(t, clearPatch)
}

func TestRequestUserInputApprovalKeepsTerminalWaitOverlayUntilLastResolve(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"},
			AgentID:      "agent-1",
		},
	}
	turnHeader := sharedto.TurnHeader{
		AgentHeader:  header.AgentHeader,
		TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-1"},
	}

	svc.applyToolApprovalRequested(tooldto.ToolApprovalRequested{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-1", ToolName: "shell"},
			ApprovalID:     "approval-1",
		},
		Kind: "request_user_input",
	})
	svc.applyToolApprovalRequested(tooldto.ToolApprovalRequested{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-2", ToolName: "shell"},
			ApprovalID:     "approval-2",
		},
		Kind: "request_user_input",
	})

	sidebar := mustGetSidebar(t, svc, "GetSidebar() after request")
	thread := mustThread(t, sidebar.Threads, "thread-1")
	assertThreadOverlay(t, thread, overlayTypeTerminalWait, "等待终端输入", overlayPriorityTerminalWait, "thread overlay after request")

	svc.applyToolApprovalResolved(tooldto.ToolApprovalResolved{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-1", ToolName: "shell"},
			ApprovalID:     "approval-1",
		},
		Kind:     "request_user_input",
		Approved: true,
	})

	sidebar = mustGetSidebar(t, svc, "GetSidebar() after first resolve")
	thread = mustThread(t, sidebar.Threads, "thread-1")
	assertThreadOverlay(t, thread, overlayTypeTerminalWait, "等待终端输入", overlayPriorityTerminalWait, "thread overlay after first resolve")

	svc.applyToolApprovalResolved(tooldto.ToolApprovalResolved{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-2", ToolName: "shell"},
			ApprovalID:     "approval-2",
		},
		Kind:     "request_user_input",
		Approved: true,
	})

	sidebar = mustGetSidebar(t, svc, "GetSidebar() after second resolve")
	thread = mustThread(t, sidebar.Threads, "thread-1")
	assertThreadOverlay(t, thread, "", "", 0, "thread overlay after second resolve")
}

func TestGenericApprovalDoesNotSetTerminalWaitOverlay(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-2"},
			AgentID:      "agent-2",
		},
	}
	turnHeader := sharedto.TurnHeader{
		AgentHeader:  header.AgentHeader,
		TurnIDHeader: sharedto.TurnIDHeader{TurnID: "turn-2"},
	}

	svc.applyToolApprovalRequested(tooldto.ToolApprovalRequested{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-2", ToolName: "shell"},
			ApprovalID:     "approval-2",
		},
	})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	thread := mustThread(t, sidebar.Threads, "thread-2")
	if thread.OverlayType != "" || thread.OverlayText != "" || thread.OverlayPriority != 0 {
		t.Fatalf("thread overlay = %#v, want no terminal wait overlay for generic approval", thread)
	}
}

func mustGetUIState(t *testing.T, svc *service, ctx context.Context, label string) *UIState {
	t.Helper()
	state, err := svc.GetState(ctx)
	if err != nil {
		t.Fatalf("%s error = %v", label, err)
	}
	return state
}

func mustGetSidebar(t *testing.T, svc *service, label string) *Sidebar {
	t.Helper()
	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("%s error = %v", label, err)
	}
	return sidebar
}

func assertOverlayThreadStatus(t *testing.T, thread ThreadSummary, wantState, wantStatus, label string) {
	t.Helper()
	if thread.State != wantState || thread.ThreadStatus != wantStatus {
		t.Fatalf("%s = %q/%q, want %s/%s", label, thread.State, thread.ThreadStatus, wantState, wantStatus)
	}
}

func assertThreadOverlay(t *testing.T, thread ThreadSummary, wantType, wantText string, wantPriority int, label string) {
	t.Helper()
	if thread.OverlayType != wantType || thread.OverlayText != wantText || thread.OverlayPriority != wantPriority {
		t.Fatalf("%s = %#v, want type=%q text=%q priority=%d", label, thread, wantType, wantText, wantPriority)
	}
}

func assertStringMapValue(t *testing.T, values map[string]string, key, want, label string) {
	t.Helper()
	if got := values[key]; got != want {
		t.Fatalf("%s = %q, want %q", label, got, want)
	}
}

func assertSidebarStatusText(t *testing.T, sidebar *Sidebar, threadID, status, header, details string) {
	t.Helper()
	assertStringMapValue(t, sidebar.Statuses, threadID, status, "sidebar.Statuses["+threadID+"]")
	assertStringMapValue(t, sidebar.StatusHeadersByThread, threadID, header, "sidebar.StatusHeadersByThread["+threadID+"]")
	assertStringMapValue(t, sidebar.StatusDetailsByThread, threadID, details, "sidebar.StatusDetailsByThread["+threadID+"]")
}

func assertRuntimeSnapshotStatus(t *testing.T, state *UIState) {
	t.Helper()
	assertStringMapValue(t, state.Statuses, "thread-1", "running", "state.Statuses[thread-1]")
	if !state.InterruptibleByThread["thread-1"] {
		t.Fatal("state.InterruptibleByThread[thread-1] = false, want true")
	}
	assertStringMapValue(t, state.StatusHeadersByThread, "thread-1", "工作中", "state.StatusHeadersByThread[thread-1]")
	assertStringMapValue(t, state.StatusDetailsByThread, "thread-1", "正在处理", "state.StatusDetailsByThread[thread-1]")
	if state.MainAgentState != "running" {
		t.Fatalf("state.MainAgentState = %q, want running", state.MainAgentState)
	}
}

func assertRuntimeSnapshotMaps(t *testing.T, state *UIState, startedAt time.Time) {
	t.Helper()
	assertRuntimeTokenUsage(t, state.TokenUsageByThread["thread-1"])
	assertRuntimeMapValue(t, state.AgentRuntimeByID["thread-1"], "provider", "claude", "state.AgentRuntimeByID[thread-1]")
	assertRuntimeMapValue(t, state.AgentRuntimeByID["thread-1"], "providerThreadId", "provider-1", "state.AgentRuntimeByID[thread-1]")
	assertRuntimeMapValue(t, state.AgentMetaByID["thread-1"], "alias", "主线程", "state.AgentMetaByID[thread-1]")
	assertRuntimeMapValue(t, state.AgentMetaByID["thread-1"], "lastActiveAt", startedAt.Format(time.RFC3339Nano), "state.AgentMetaByID[thread-1]")
	assertEmptyThreadAlerts(t, state)
}

func assertRuntimeTokenUsage(t *testing.T, usage *uidto.ThreadPatchTokenUsage) {
	t.Helper()
	if usage == nil {
		t.Fatal("state.TokenUsageByThread[thread-1] = nil, want 53/200/26.5")
	}
	if usage.UsedTokens != 53 || usage.ContextWindowTokens != 200 || usage.UsedPercent != 26.5 {
		t.Fatalf("state.TokenUsageByThread[thread-1] = %#v, want 53/200/26.5", usage)
	}
}

func assertRuntimeMapValue(t *testing.T, values map[string]any, key string, want any, label string) {
	t.Helper()
	if values[key] != want {
		t.Fatalf("%s = %#v, want %s=%#v", label, values, key, want)
	}
}

func assertEmptyThreadAlerts(t *testing.T, state *UIState) {
	t.Helper()
	alerts, ok := state.AlertsByThread["thread-1"]
	if !ok || len(alerts) != 0 {
		t.Fatalf("state.AlertsByThread[thread-1] = %#v, want empty slice", alerts)
	}
}

func assertLaunchOverlayPatch(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	if patch.Status != "starting" || patch.StatusHeader != "MCP 启动中" || patch.StatusDetails != "正在初始化 MCP 服务" {
		t.Fatalf("launch patch = %#v", patch)
	}
	if patch.OverlayType != overlayTypeMCPStartup || patch.OverlayText != "MCP 启动中" || patch.OverlayPriority != overlayPriorityMCPStartup {
		t.Fatalf("launch patch overlay = %#v", patch)
	}
}

func assertClearOverlayPatch(t *testing.T, patch uidto.UIThreadPatch) {
	t.Helper()
	if patch.OverlayType != "" || patch.OverlayText != "" || patch.OverlayPriority != 0 {
		t.Fatalf("clear patch overlay = %#v, want cleared", patch)
	}
	if patch.StatusHeader == "" {
		t.Fatalf("clear patch presentation = %#v, want non-empty header", patch)
	}
}

func mustNewUIStateService(t *testing.T) *service {
	t.Helper()

	svc, _, err := NewService(testLoggerRuntime(), nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return svc
}

func mustThread(t *testing.T, threads []ThreadSummary, id string) ThreadSummary {
	t.Helper()

	for _, thread := range threads {
		if thread.ID == id {
			return thread
		}
	}
	t.Fatalf("thread %q not found in %#v", id, threads)
	return ThreadSummary{}
}
