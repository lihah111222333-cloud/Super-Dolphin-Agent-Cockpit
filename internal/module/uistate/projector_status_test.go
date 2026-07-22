package uistate

import (
	"context"
	"testing"
	"time"

	"github.com/kelindar/event"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestDerivedThreadStatusTransitions(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	header := testAgentSessionHeader("thread-1", "agent-1")
	turnHeader := testTurnHeader(header, "turn-1")

	svc.applyAgentLaunched(agentdto.AgentLaunched{AgentSessionHeader: header, CWD: "/tmp/demo"})
	svc.applyAgentStateChanged(agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_running"})
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnHeader})
	assertThreadStatus(t, svc, "thinking")

	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "command_execution", Command: "ls"})
	assertThreadStatus(t, svc, "running")

	svc.applyItemCompleted(turndto.ItemCompleted{TurnHeader: turnHeader, ItemType: "command_execution", Command: "ls"})
	assertThreadStatus(t, svc, "thinking")

	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "file_change", File: "main.go"})
	assertThreadStatus(t, svc, "editing")

	svc.applyItemCompleted(turndto.ItemCompleted{TurnHeader: turnHeader, ItemType: "file_change", File: "main.go"})
	assertThreadStatus(t, svc, "thinking")

	svc.applyToolApprovalRequested(tooldto.ToolApprovalRequested{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-1", ToolName: "shell"},
			ApprovalID:     "approval-1",
		},
	})
	assertThreadStatus(t, svc, "waiting")

	svc.applyToolApprovalResolved(tooldto.ToolApprovalResolved{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-1", ToolName: "shell"},
			ApprovalID:     "approval-1",
		},
		Approved: true,
	})
	assertThreadStatus(t, svc, "thinking")
}

// TestTimelineAndProjectionTerminalStatusParity 保证主 UI 投影不再私有化终态推导。
func TestTimelineAndProjectionTerminalStatusParity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		terminal turndto.TurnTerminalV2
		want     string
	}{
		{
			name:     "completed without explicit status",
			terminal: turndto.TurnTerminalV2{Outcome: "success"},
			want:     "completed",
		},
		{
			name:     "failed without provider diagnostic",
			terminal: turndto.TurnTerminalV2{Outcome: "failed"},
			want:     "failed",
		},
		{
			name:     "canonical interrupted status",
			terminal: turndto.TurnTerminalV2{Outcome: "interrupted"},
			want:     "interrupted",
		},
		{
			name:     "canonical cancelled status",
			terminal: turndto.TurnTerminalV2{Outcome: "cancelled"},
			want:     "cancelled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := completionStatus(tt.terminal); got != tt.want {
				t.Fatalf("completionStatus(%+v) = %q, want canonical terminal status %q", tt.terminal, got, tt.want)
			}
		})
	}
}

func TestDeriveInterruptibleRequiresActiveTurnIdentityForSidebarSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		activeTurn *TurnSummary
		statuses   map[string]string
		want       map[string]bool
	}{
		{
			name:     "sidebar snapshot rejects status-only running thread without active turn",
			statuses: map[string]string{"thread-1": "running"},
			want:     map[string]bool{"thread-1": false},
		},
		{
			name:       "sidebar snapshot allows only the matching active turn thread",
			activeTurn: &TurnSummary{ID: "turn-1", ThreadID: "thread-1", Status: "running"},
			statuses:   map[string]string{"thread-1": "running", "thread-2": "running"},
			want:       map[string]bool{"thread-1": true, "thread-2": false},
		},
		{
			name:       "sidebar snapshot rejects active turn on another thread",
			activeTurn: &TurnSummary{ID: "turn-1", ThreadID: "thread-other", Status: "running"},
			statuses:   map[string]string{"thread-1": "running"},
			want:       map[string]bool{"thread-1": false},
		},
		{
			name:       "sidebar snapshot rejects empty active turn id",
			activeTurn: &TurnSummary{ThreadID: "thread-1", Status: "running"},
			statuses:   map[string]string{"thread-1": "running"},
			want:       map[string]bool{"thread-1": false},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// This is only the sidebar snapshot gate. Thread patch payloads and
			// frontend click controls have independent interruptibility chains.
			sidebar := &Sidebar{
				ActiveTurn:            tt.activeTurn,
				Statuses:              tt.statuses,
				InterruptibleByThread: map[string]bool{},
			}

			deriveInterruptible(sidebar)

			for threadID, want := range tt.want {
				if got := sidebar.InterruptibleByThread[threadID]; got != want {
					t.Fatalf("sidebar snapshot InterruptibleByThread[%s] = %v, want %v", threadID, got, want)
				}
			}
		})
	}
}

func TestDerivedThreadStatusTreatsToolAndCollabAsRunning(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	header := testAgentSessionHeader("thread-2", "agent-2")
	turnHeader := testTurnHeader(header, "turn-2")

	svc.applyAgentStateChanged(agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_running"})
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnHeader})

	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "tool-1", ToolName: "shell"},
	})
	assertThreadStatus(t, svc, "running")

	svc.applyToolCallEnd(tooldto.ToolCallEnd{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "tool-1", ToolName: "shell"},
		Success:        true,
	})
	assertThreadStatus(t, svc, "thinking")

	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "tool-2", ToolName: "spawn_agent"},
	})
	assertThreadStatus(t, svc, "running")

	svc.applyToolCallEnd(tooldto.ToolCallEnd{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "tool-2", ToolName: "spawn_agent"},
		Success:        true,
	})
	assertThreadStatus(t, svc, "thinking")
}

func TestProjectionSubscriptionsDeriveWaitingStatus(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc := newProjectionTestService(t)
	svc.bindDispatcher(dispatcher)
	cancels := registerProjectionSubscriptions(dispatcher, svc)
	defer cancelAll(cancels)

	header := testAgentSessionHeader("thread-3", "agent-3")
	turnHeader := testTurnHeader(header, "turn-3")
	event.Publish(dispatcher, agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_running"})
	event.Publish(dispatcher, turndto.TurnStarted{TurnHeader: turnHeader})
	event.Publish(dispatcher, tooldto.ToolApprovalRequested{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-3", ToolName: "shell"},
			ApprovalID:     "approval-3",
		},
	})

	sidebar := waitForSidebarState(t, svc, func(sidebar *Sidebar) bool {
		return sidebar.Statuses["thread-3"] == "waiting"
	})
	if got := sidebar.Statuses["thread-3"]; got != "waiting" {
		t.Fatalf("sidebar.Statuses[thread-3] = %q, want waiting", got)
	}
}

func TestThreadPatchUsesDerivedMainAgentState(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	header := testAgentSessionHeader("thread-4", "agent-main")
	turnHeader := testTurnHeader(header, "turn-4")

	svc.applyAgentStateChanged(agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_running"})
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnHeader})
	if err := svc.SetPreference(context.Background(), preferenceMainAgentID, "agent-main"); err != nil {
		t.Fatalf("SetPreference(mainAgentId) error = %v", err)
	}

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-4", "turn/started")
	svc.mu.Unlock()
	if patch.MainAgentState != "thinking" {
		t.Fatalf("patch.MainAgentState = %q, want thinking", patch.MainAgentState)
	}
}

func TestActivityHandlersIgnoreEmptyThreadID(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	header := testAgentSessionHeader("", "agent-empty")
	turnHeader := testTurnHeader(header, "turn-empty")

	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "command_execution", Command: "ls"})
	svc.applyItemCompleted(turndto.ItemCompleted{TurnHeader: turnHeader, ItemType: "command_execution", Command: "ls"})
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "tool-empty", ToolName: "shell"},
	})
	svc.applyToolCallEnd(tooldto.ToolCallEnd{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "tool-empty", ToolName: "shell"},
		Success:        true,
	})
	svc.applyToolApprovalRequested(tooldto.ToolApprovalRequested{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "approval-empty", ToolName: "shell"},
			ApprovalID:     "approval-empty",
		},
	})
	svc.applyToolApprovalResolved(tooldto.ToolApprovalResolved{
		ToolApprovalHeader: sharedto.ToolApprovalHeader{
			ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "approval-empty", ToolName: "shell"},
			ApprovalID:     "approval-empty",
		},
		Approved: true,
	})

	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if got := len(svc.state.Threads); got != 0 {
		t.Fatalf("thread summaries = %#v, want no thread updates", svc.state.Threads)
	}
	if got := len(svc.activityByThread); got != 0 {
		t.Fatalf("activityByThread = %#v, want empty", svc.activityByThread)
	}
}

func TestAgentStateChangesAreNotPreservedAsIdle(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.state.Threads = []ThreadSummary{{
		ID:           "thread-5",
		AgentID:      "agent-5",
		State:        "idle",
		ThreadStatus: "idle",
	}}
	svc.state.Agents = []AgentSummary{{
		ID:         "agent-5",
		ThreadID:   "thread-5",
		State:      "idle",
		AgentState: "idle",
	}}
	header := testAgentSessionHeader("thread-5", "agent-5")

	svc.applyAgentStateChanged(agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_queued"})
	assertThreadStatus(t, svc, "starting")

	svc.applyAgentStateChanged(agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_starting"})
	assertThreadStatus(t, svc, "thinking")

	svc.applyAgentStateChanged(agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_running"})
	assertThreadStatus(t, svc, "running")
}

func TestAgentStoppedClearsActiveTurnBeforeSidebarProjection(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	header := testAgentSessionHeader("thread-stopped", "agent-stopped")
	turnHeader := testTurnHeader(header, "turn-stopped")

	svc.applyAgentStateChanged(agentdto.StateChanged{AgentSessionHeader: header, NewState: "turn_running"})
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnHeader})
	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "command_execution", Command: "ls"})

	runningSidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() before stop error = %v", err)
	}
	if got := runningSidebar.Statuses["thread-stopped"]; got != "running" {
		t.Fatalf("sidebar.Statuses[thread-stopped] before stop = %q, want running", got)
	}

	svc.applyAgentStopped(agentdto.AgentStopped{AgentSessionHeader: header})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() after stop error = %v", err)
	}
	if sidebar.ActiveTurn != nil {
		t.Fatalf("sidebar.ActiveTurn after stop = %#v, want nil", sidebar.ActiveTurn)
	}
	if got := sidebar.Statuses["thread-stopped"]; got == "running" {
		t.Fatalf("sidebar.Statuses[thread-stopped] after stop = %q, want terminal non-running status", got)
	}
	if got := sidebar.StatusHeadersByThread["thread-stopped"]; got == "工作中" {
		t.Fatalf("StatusHeadersByThread[thread-stopped] after stop = %q, want non-working header", got)
	}

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-stopped", "agent/stopped")
	svc.mu.Unlock()
	if patch.Status == "running" {
		t.Fatalf("agent stopped patch status = %q, want non-running", patch.Status)
	}
	if patch.StatusHeader == "工作中" {
		t.Fatalf("agent stopped patch header = %q, want non-working header", patch.StatusHeader)
	}
}

func TestFailedThreadStoppedKeepsThreadAndReason(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.state.Threads = []ThreadSummary{{
		ID:           "thread-failed-start",
		AgentID:      "agent-failed-start",
		Name:         "Codex launch",
		State:        "starting",
		ThreadStatus: "starting",
	}}

	svc.applyThreadStopped(threaddto.Stopped{
		ThreadID: "thread-failed-start",
		AgentID:  "agent-failed-start",
		Status:   "failed",
		Reason:   "codex provider start failed",
	})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if len(sidebar.Threads) != 1 || sidebar.Threads[0].ID != "thread-failed-start" {
		t.Fatalf("sidebar threads = %#v, want failed thread preserved", sidebar.Threads)
	}
	if got := sidebar.Statuses["thread-failed-start"]; got != "error" {
		t.Fatalf("sidebar status = %q, want error", got)
	}
	if got := sidebar.StatusDetailsByThread["thread-failed-start"]; got != "codex provider start failed" {
		t.Fatalf("sidebar status details = %q, want provider failure reason", got)
	}

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-failed-start", "thread/stopped")
	svc.mu.Unlock()
	if patch.Status != "error" || patch.StatusDetails != "codex provider start failed" {
		t.Fatalf("failed stopped patch = %+v, want error status with provider failure details", patch)
	}
}

func TestArchivedThreadStatusWinsOverStaleActiveTurn(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.state.Threads = []ThreadSummary{{
		ID:              "thread-archived-active",
		AgentID:         "agent-archived-active",
		State:           "archived",
		ThreadStatus:    "archived",
		LifecycleStatus: "archived",
	}}
	svc.state.Agents = []AgentSummary{{
		ID:           "agent-archived-active",
		ThreadID:     "thread-archived-active",
		State:        "running",
		AgentState:   "running",
		ThreadStatus: "running",
	}}
	svc.state.ActiveTurn = &TurnSummary{ThreadID: "thread-archived-active", Status: "running"}

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if got := sidebar.Statuses["thread-archived-active"]; got != "archived" {
		t.Fatalf("sidebar.Statuses[thread-archived-active] = %q, want archived", got)
	}
	if got := sidebar.StatusHeadersByThread["thread-archived-active"]; got == "工作中" {
		t.Fatalf("StatusHeadersByThread[thread-archived-active] = %q, want non-working header", got)
	}

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-archived-active", "thread/archived")
	svc.mu.Unlock()
	if patch.Status != "archived" {
		t.Fatalf("archived patch status = %q, want archived", patch.Status)
	}
	if patch.StatusHeader == "工作中" {
		t.Fatalf("archived patch header = %q, want non-working header", patch.StatusHeader)
	}
}

func TestArchivedThreadStatusSurvivesLaterAgentStopped(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	header := testAgentSessionHeader("thread-archived-stop", "agent-archived-stop")

	svc.applyThreadStopped(threaddto.Stopped{
		ThreadID: "thread-archived-stop",
		AgentID:  "agent-archived-stop",
		Status:   "archived",
		Reason:   "archived",
	})
	svc.applyAgentStopped(agentdto.AgentStopped{
		AgentSessionHeader: header,
		Reason:             "archived",
	})

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
	if got := sidebar.Statuses["thread-archived-stop"]; got != "archived" {
		t.Fatalf("sidebar.Statuses[thread-archived-stop] = %q, want archived", got)
	}
	if got := sidebar.StatusHeadersByThread["thread-archived-stop"]; got != "已归档" {
		t.Fatalf("StatusHeadersByThread[thread-archived-stop] = %q, want 已归档", got)
	}

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-archived-stop", "agent/stopped")
	svc.mu.Unlock()
	if patch.Status != "archived" {
		t.Fatalf("agent stopped patch status = %q, want archived", patch.Status)
	}
	if patch.StatusHeader != "已归档" {
		t.Fatalf("agent stopped patch header = %q, want 已归档", patch.StatusHeader)
	}
}

func newProjectionTestService(t *testing.T) *service {
	t.Helper()

	svc, _, err := NewService(nil, nil, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	return svc
}

func testAgentSessionHeader(threadID, agentID string) sharedto.AgentSessionHeader {
	return sharedto.AgentSessionHeader{
		AgentHeader: sharedto.AgentHeader{
			ThreadHeader: sharedto.ThreadHeader{
				EventHeader: sharedto.EventHeader{Timestamp: time.Now()},
				ThreadID:    threadID,
			},
			AgentID: agentID,
		},
		SessionID: threadID,
	}
}

func testTurnHeader(header sharedto.AgentSessionHeader, turnID string) sharedto.TurnHeader {
	return sharedto.TurnHeader{
		AgentHeader: header.AgentHeader,
		TurnIDHeader: sharedto.TurnIDHeader{
			TurnID: turnID,
		},
	}
}

func assertThreadStatus(t *testing.T, svc *service, want string) {
	t.Helper()

	svc.mu.RLock()
	defer svc.mu.RUnlock()
	for _, item := range svc.state.Threads {
		if item.ID == "" {
			continue
		}
		if item.ThreadStatus != want {
			t.Fatalf("thread %s status = %q, want %q", item.ID, item.ThreadStatus, want)
		}
		return
	}
	t.Fatal("expected thread summary")
}

func cancelAll(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func TestApplyTurnStartedClearsLastMessageRegression(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	header := testAgentSessionHeader("thread-stream", "agent-stream")

	// Inject a previous turn with a LastMessage left behind
	svc.state.Threads = []ThreadSummary{{
		ID:          "thread-stream",
		AgentID:     "agent-stream",
		LastMessage: "上一轮残留的流式消息",
	}}

	// Start a new turn
	turnHeader := testTurnHeader(header, "turn-stream-2")
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnHeader})

	// Verify that LastMessage is cleanly wiped out so it doesn't leak into the new turn
	svc.mu.RLock()
	defer svc.mu.RUnlock()
	for _, item := range svc.state.Threads {
		if item.ID == "thread-stream" {
			if item.LastMessage != "" {
				t.Fatalf("LastMessage was not cleared, got %q, want empty string", item.LastMessage)
			}
			return
		}
	}
	t.Fatal("expected thread-stream to exist")
}

func TestApplyTurnStartedAllowsSameTurnIDOnDistinctCanonicalThreads(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.state.RecentTurns = []TurnSummary{{
		ID:       "turn-shared",
		ThreadID: "thread-finished",
		Status:   "completed",
	}}

	header := testAgentSessionHeader("thread-active", "agent-active")
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: testTurnHeader(header, "turn-shared")})

	svc.mu.RLock()
	defer svc.mu.RUnlock()
	if svc.state.ActiveTurn == nil || svc.state.ActiveTurn.ID != "turn-shared" || svc.state.ActiveTurn.ThreadID != "thread-active" {
		t.Fatalf("active turn = %#v, want turn-shared on thread-active", svc.state.ActiveTurn)
	}
	if svc.activityByThread["thread-active"] == nil {
		t.Fatalf("activityByThread = %#v, want active thread activity", svc.activityByThread)
	}
}

func TestStaleTurnCompletedDoesNotOverwriteCurrentProjection(t *testing.T) {
	t.Parallel()

	assertStaleTurnTerminalPreservesCurrentProjection(t, func(svc *service, header sharedto.TurnHeader) {
		svc.applyTurnCompleted(canonicalPatchTurnCompleted(t, turndto.TurnCompleted{
			TurnHeader: header,
			Success:    true,
			Status:     "completed",
		}))
	})
}

func TestStaleTurnInterruptedDoesNotOverwriteCurrentProjection(t *testing.T) {
	t.Parallel()

	assertStaleTurnTerminalPreservesCurrentProjection(t, func(svc *service, header sharedto.TurnHeader) {
		svc.applyTurnInterrupted(turndto.TurnInterrupted{
			TurnHeader: header,
			Reason:     "late interrupt",
		})
	})
}

func TestLateTerminalAfterCurrentTerminalDoesNotOverwriteProjection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		applyLate func(*testing.T, *service, sharedto.TurnHeader)
	}{
		{
			name: "completed",
			applyLate: func(t *testing.T, svc *service, header sharedto.TurnHeader) {
				svc.applyTurnCompleted(canonicalPatchTurnCompleted(t, turndto.TurnCompleted{
					TurnHeader: header,
					Success:    true,
					Status:     "completed",
				}))
			},
		},
		{
			name: "interrupted",
			applyLate: func(_ *testing.T, svc *service, header sharedto.TurnHeader) {
				svc.applyTurnInterrupted(turndto.TurnInterrupted{
					TurnHeader: header,
					Reason:     "late interrupt",
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertLateTerminalAfterCurrentTerminalPreservesProjection(t, tt.applyLate)
		})
	}
}

func assertLateTerminalAfterCurrentTerminalPreservesProjection(t *testing.T, applyLate func(*testing.T, *service, sharedto.TurnHeader)) {
	t.Helper()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := newProjectionTestService(t)
	svc.bindDispatcher(dispatcher)
	patches := subscribeThreadPatch(t, dispatcher)
	header := testAgentSessionHeader("thread-current", "agent-current")
	turnA := testTurnHeader(header, "turn-a")
	turnB := testTurnHeader(header, "turn-b")

	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnA})
	_ = mustReceiveThreadPatch(t, patches)
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: turnB})
	_ = mustReceiveThreadPatch(t, patches)
	svc.applyTurnCompleted(canonicalPatchTurnCompleted(t, turndto.TurnCompleted{
		TurnHeader: turnB,
		Success:    false,
		Status:     "failed",
		Error:      "current terminal",
	}))
	_ = mustReceiveThreadPatch(t, patches)
	setCurrentTerminalProjectionSentinel(t, svc)

	applyLate(t, svc, turnA)
	assertNoThreadPatch(t, patches, "late terminal after current terminal")
	assertCurrentTerminalProjectionPreserved(t, svc)
}

func setCurrentTerminalProjectionSentinel(t *testing.T, svc *service) {
	t.Helper()

	svc.mu.Lock()
	defer svc.mu.Unlock()
	if svc.state.ActiveTurn != nil {
		t.Fatalf("active turn after current terminal = %#v, want nil", svc.state.ActiveTurn)
	}
	svc.state.Threads = []ThreadSummary{{
		ID:              "thread-current",
		AgentID:         "agent-current",
		State:           "failed",
		ThreadStatus:    "failed",
		OverlayType:     overlayTypeTerminalWait,
		OverlayText:     "current terminal overlay",
		OverlayPriority: overlayPriorityTerminalWait,
	}}
	if svc.activityByThread == nil {
		svc.activityByThread = map[string]*threadActivity{}
	}
	svc.activityByThread["thread-current"] = &threadActivity{turnDepth: 1, commandDepth: 1}
}

func assertCurrentTerminalProjectionPreserved(t *testing.T, svc *service) {
	t.Helper()

	svc.mu.RLock()
	defer svc.mu.RUnlock()
	activity := svc.activityByThread["thread-current"]
	if activity == nil || activity.turnDepth != 1 || activity.commandDepth != 1 {
		t.Fatalf("current thread activity = %#v, want preserved sentinel", activity)
	}
	if len(svc.state.Threads) != 1 || svc.state.Threads[0].ThreadStatus != "failed" || svc.state.Threads[0].OverlayType != overlayTypeTerminalWait {
		t.Fatalf("current thread projection = %#v, want preserved current terminal projection", svc.state.Threads)
	}
	seen := map[string]bool{}
	for _, turn := range svc.state.RecentTurns {
		seen[turn.ID] = true
	}
	if !seen["turn-a"] || !seen["turn-b"] {
		t.Fatalf("recent turns = %#v, want both current and late terminal history", svc.state.RecentTurns)
	}
}

func assertStaleTurnTerminalPreservesCurrentProjection(t *testing.T, apply func(*service, sharedto.TurnHeader)) {
	t.Helper()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := newProjectionTestService(t)
	svc.bindDispatcher(dispatcher)
	patches := subscribeThreadPatch(t, dispatcher)
	header := testAgentSessionHeader("thread-active", "agent-active")
	svc.applyTurnStarted(turndto.TurnStarted{TurnHeader: testTurnHeader(header, "turn-active")})
	_ = mustReceiveThreadPatch(t, patches)

	svc.mu.Lock()
	svc.state.Threads = []ThreadSummary{{
		ID:              "thread-active",
		AgentID:         "agent-active",
		State:           "running",
		ThreadStatus:    "running",
		OverlayType:     overlayTypeTerminalWait,
		OverlayText:     "等待终端输入",
		OverlayPriority: overlayPriorityTerminalWait,
	}}
	svc.mu.Unlock()

	apply(svc, testTurnHeader(header, "turn-stale"))
	assertNoThreadPatch(t, patches, "stale turn terminal")

	svc.mu.RLock()
	defer svc.mu.RUnlock()
	assertCurrentActiveTurn(t, svc.state.ActiveTurn)
	assertCurrentThreadActivity(t, svc.activityByThread["thread-active"])
	assertCurrentThreadProjection(t, svc.state.Threads)
	assertStaleTerminalRecentTurn(t, svc.state.RecentTurns)
}

func assertCurrentActiveTurn(t *testing.T, activeTurn *TurnSummary) {
	t.Helper()

	if activeTurn == nil || activeTurn.ID != "turn-active" || activeTurn.ThreadID != "thread-active" {
		t.Fatalf("active turn = %#v, want current active turn preserved", activeTurn)
	}
}

func assertCurrentThreadActivity(t *testing.T, activity *threadActivity) {
	t.Helper()

	if activity == nil || activity.turnDepth != 1 {
		t.Fatalf("current thread activity = %#v, want active turn depth", activity)
	}
}

func assertCurrentThreadProjection(t *testing.T, threads []ThreadSummary) {
	t.Helper()

	if len(threads) != 1 || threads[0].ThreadStatus != "running" || threads[0].OverlayType != overlayTypeTerminalWait {
		t.Fatalf("current thread projection = %#v, want running thread with terminal overlay", threads)
	}
}

func assertStaleTerminalRecentTurn(t *testing.T, recentTurns []TurnSummary) {
	t.Helper()

	if len(recentTurns) != 1 || recentTurns[0].ID != "turn-stale" {
		t.Fatalf("recent turns = %#v, want independent stale terminal record", recentTurns)
	}
}
