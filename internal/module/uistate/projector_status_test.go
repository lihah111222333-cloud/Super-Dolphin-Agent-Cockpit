package uistate

import (
	"context"
	"testing"
	"time"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/kelindar/event"
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
	time.Sleep(20 * time.Millisecond)

	sidebar, err := svc.GetSidebar(context.Background())
	if err != nil {
		t.Fatalf("GetSidebar() error = %v", err)
	}
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
