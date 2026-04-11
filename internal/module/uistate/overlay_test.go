package uistate

import (
	"context"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	uidto "github.com/anthropic-ai/super-agent-v3/internal/dto/ui"
	"github.com/kelindar/event"
)

func TestGetStatePrefersOverlayUntilTTLExpires(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", State: "idle", ThreadStatus: "idle"}}

	svc.mu.Lock()
	svc.setThreadOverlayLocked("thread-1", overlayTypeMCPStartup, "", overlayPriorityMCPStartup, 5*time.Millisecond)
	svc.mu.Unlock()

	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	thread := mustThread(t, state.Threads, "thread-1")
	if thread.State != "starting" || thread.ThreadStatus != "starting" {
		t.Fatalf("thread status = %q/%q, want starting/starting", thread.State, thread.ThreadStatus)
	}
	if thread.OverlayType != overlayTypeMCPStartup || thread.OverlayText != "MCP 启动中" || thread.OverlayPriority != overlayPriorityMCPStartup {
		t.Fatalf("thread overlay = %#v, want startup overlay", thread)
	}

	time.Sleep(20 * time.Millisecond)

	state, err = svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() after TTL error = %v", err)
	}
	thread = mustThread(t, state.Threads, "thread-1")
	if thread.State != "idle" || thread.ThreadStatus != "idle" {
		t.Fatalf("thread status after TTL = %q/%q, want idle/idle", thread.State, thread.ThreadStatus)
	}
	if thread.OverlayType != "" || thread.OverlayText != "" || thread.OverlayPriority != 0 {
		t.Fatalf("thread overlay after TTL = %#v, want cleared", thread)
	}
}

func TestGetSidebarKeepsHigherPriorityOverlay(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", State: "idle", ThreadStatus: "idle"}}

	svc.mu.Lock()
	svc.setThreadOverlayLocked("thread-1", overlayTypeTerminalWait, "等待终端输入", 90, time.Minute)
	svc.setThreadOverlayLocked("thread-1", overlayTypeMCPStartup, "", overlayPriorityMCPStartup, time.Minute)
	svc.mu.Unlock()

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	thread := mustThread(t, sidebar.Threads, "thread-1")
	if thread.OverlayType != overlayTypeTerminalWait || thread.OverlayText != "等待终端输入" || thread.OverlayPriority != 90 {
		t.Fatalf("thread overlay = %#v, want terminal wait overlay", thread)
	}
	if thread.State != "waiting" || thread.ThreadStatus != "waiting" {
		t.Fatalf("thread status = %q/%q, want waiting/waiting", thread.State, thread.ThreadStatus)
	}
	if got := sidebar.Statuses["thread-1"]; got != "waiting" {
		t.Fatalf("sidebar.Statuses[thread-1] = %q, want waiting", got)
	}
	if got := sidebar.StatusHeadersByThread["thread-1"]; got != "等待终端输入" {
		t.Fatalf("sidebar.StatusHeadersByThread[thread-1] = %q, want 等待终端输入", got)
	}
	if got := sidebar.StatusDetailsByThread["thread-1"]; got != "命令正在等待终端输入" {
		t.Fatalf("sidebar.StatusDetailsByThread[thread-1] = %q, want 命令正在等待终端输入", got)
	}
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

	before, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() before ready error = %v", err)
	}
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

	after, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after ready error = %v", err)
	}
	thread := mustThread(t, after.Threads, "thread-1")
	if thread.OverlayType != "" || thread.OverlayText != "" || thread.OverlayPriority != 0 {
		t.Fatalf("thread overlay after ready = %#v, want cleared", thread)
	}
	if got := after.Statuses["thread-1"]; got != "idle" {
		t.Fatalf("sidebar.Statuses[thread-1] = %q, want idle", got)
	}
	if got := after.StatusHeadersByThread["thread-1"]; got != "等待指示" {
		t.Fatalf("sidebar.StatusHeadersByThread[thread-1] = %q, want 等待指示", got)
	}
}

func TestGetStateIncludesRuntimeSnapshotContractFields(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	startedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc.state.Threads = []ThreadSummary{{ID: "thread-1", Name: "主线程", AgentID: "agent-main", State: "running", ThreadStatus: "running", AgentState: "running", LastMessage: "正在处理"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-main", ThreadID: "thread-1", Provider: "claude", ProviderThreadID: "provider-1", CWD: "/repo", State: "running", AgentState: "running"}}
	svc.state.RecentTurns = []TurnSummary{{ID: "turn-1", AgentID: "agent-main", ThreadID: "thread-1", Status: "running", StartedAt: &startedAt}}
	svc.state.TokenUsage = TokenUsage{TotalTokens: 53, ContextWindowTokens: 200}
	svc.state.MainAgentID = "agent-main"

	state, err := svc.GetState(withDiffStateRequest(context.Background(), "thread-1", false, 0))
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	if got := state.Statuses["thread-1"]; got != "running" {
		t.Fatalf("state.Statuses[thread-1] = %q, want running", got)
	}
	if !state.InterruptibleByThread["thread-1"] {
		t.Fatal("state.InterruptibleByThread[thread-1] = false, want true")
	}
	if got := state.StatusHeadersByThread["thread-1"]; got != "工作中" {
		t.Fatalf("state.StatusHeadersByThread[thread-1] = %q, want 工作中", got)
	}
	if got := state.StatusDetailsByThread["thread-1"]; got != "正在处理" {
		t.Fatalf("state.StatusDetailsByThread[thread-1] = %q, want 正在处理", got)
	}
	usage := state.TokenUsageByThread["thread-1"]
	if usage == nil || usage.UsedTokens != 53 || usage.ContextWindowTokens != 200 || usage.UsedPercent != 26.5 {
		t.Fatalf("state.TokenUsageByThread[thread-1] = %#v, want 53/200/26.5", usage)
	}
	if runtime := state.AgentRuntimeByID["thread-1"]; runtime["provider"] != "claude" || runtime["providerThreadId"] != "provider-1" {
		t.Fatalf("state.AgentRuntimeByID[thread-1] = %#v", runtime)
	}
	if meta := state.AgentMetaByID["thread-1"]; meta["alias"] != "主线程" || meta["lastActiveAt"] != startedAt.Format(time.RFC3339Nano) {
		t.Fatalf("state.AgentMetaByID[thread-1] = %#v", meta)
	}
	if alerts, ok := state.AlertsByThread["thread-1"]; !ok || len(alerts) != 0 {
		t.Fatalf("state.AlertsByThread[thread-1] = %#v, want empty slice", alerts)
	}
	if state.MainAgentState != "running" {
		t.Fatalf("state.MainAgentState = %q, want running", state.MainAgentState)
	}
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
	if launchPatch.Status != "starting" || launchPatch.StatusHeader != "MCP 启动中" || launchPatch.StatusDetails != "正在初始化 MCP 服务" {
		t.Fatalf("launch patch = %#v", launchPatch)
	}
	if launchPatch.OverlayType != overlayTypeMCPStartup || launchPatch.OverlayText != "MCP 启动中" || launchPatch.OverlayPriority != overlayPriorityMCPStartup {
		t.Fatalf("launch patch overlay = %#v", launchPatch)
	}

	svc.applyAgentRuntimeReported(agentdto.AgentRuntimeReported{
		AgentSessionHeader: header,
		Provider:           "claude",
		Port:               8080,
	})
	clearPatch := mustReceiveThreadPatch(t, patches)
	if clearPatch.OverlayType != "" || clearPatch.OverlayText != "" || clearPatch.OverlayPriority != 0 {
		t.Fatalf("clear patch overlay = %#v, want cleared", clearPatch)
	}
	if clearPatch.StatusHeader == "" {
		t.Fatalf("clear patch presentation = %#v, want non-empty header", clearPatch)
	}
}

func TestRequestUserInputApprovalKeepsTerminalWaitOverlayUntilLastResolve(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-1"},
			AgentID:      "agent-1",
		},
		SessionID: "session-1",
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

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after request error = %v", err)
	}
	thread := mustThread(t, sidebar.Threads, "thread-1")
	if thread.OverlayType != overlayTypeTerminalWait || thread.OverlayText != "等待终端输入" || thread.OverlayPriority != overlayPriorityTerminalWait {
		t.Fatalf("thread overlay after request = %#v, want terminal wait overlay", thread)
	}

	svc.applyToolApprovalResolved(tooldto.ToolApprovalResolved{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-1", ToolName: "shell"},
			ApprovalID:     "approval-1",
		},
		Kind:     "request_user_input",
		Approved: true,
	})

	sidebar, err = svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after first resolve error = %v", err)
	}
	thread = mustThread(t, sidebar.Threads, "thread-1")
	if thread.OverlayType != overlayTypeTerminalWait || thread.OverlayText != "等待终端输入" || thread.OverlayPriority != overlayPriorityTerminalWait {
		t.Fatalf("thread overlay after first resolve = %#v, want terminal wait overlay", thread)
	}

	svc.applyToolApprovalResolved(tooldto.ToolApprovalResolved{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-2", ToolName: "shell"},
			ApprovalID:     "approval-2",
		},
		Kind:     "request_user_input",
		Approved: true,
	})

	sidebar, err = svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after second resolve error = %v", err)
	}
	thread = mustThread(t, sidebar.Threads, "thread-1")
	if thread.OverlayType != "" || thread.OverlayText != "" || thread.OverlayPriority != 0 {
		t.Fatalf("thread overlay after second resolve = %#v, want cleared", thread)
	}
}

func TestGenericApprovalDoesNotSetTerminalWaitOverlay(t *testing.T) {
	t.Parallel()

	svc := mustNewUIStateService(t)
	header := sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{ThreadID: "thread-2"},
			AgentID:      "agent-2",
		},
		SessionID: "session-2",
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

func mustNewUIStateService(t *testing.T) *service {
	t.Helper()

	svc, _, err := NewService(nil, nil, nil, nil, nil)
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
