package uistate

import (
	"context"
	"reflect"
	"testing"

	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/uistate/timeline"
)

func TestActivityStats_CommandIncrementsCommands(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-command", "agent-1"), "turn-1")

	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "command", Command: "ls"})
	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "command", Command: "pwd", CallID: "cmd-2"})

	stats := activityStatsForThread(t, svc, "thread-stats-command")
	if stats.Commands != 2 {
		t.Fatalf("stats.Commands = %d, want 2", stats.Commands)
	}
}

func TestActivityStats_FileIncrementsFileEdits(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-file", "agent-1"), "turn-1")

	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "file", File: "main.go"})
	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "file", File: "go.mod", CallID: "file-2"})

	stats := activityStatsForThread(t, svc, "thread-stats-file")
	if stats.FileEdits != 2 {
		t.Fatalf("stats.FileEdits = %d, want 2", stats.FileEdits)
	}
}

func TestActivityStats_ToolIncrementsToolCalls(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-tool", "agent-1"), "turn-1")

	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-tool-1", ToolName: "shell"},
	})
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-tool-2", ToolName: "shell"},
	})

	stats := activityStatsForThread(t, svc, "thread-stats-tool")
	if got := stats.ToolCalls["shell"]; got != 2 {
		t.Fatalf("stats.ToolCalls[shell] = %d, want 2", got)
	}
}

func TestActivityStats_LSPToolIncrementsLSPCalls(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-lsp", "agent-1"), "turn-1")

	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-lsp-1", ToolName: "lsp_edit"},
	})
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-lsp-2", ToolName: "lsp_edit"},
	})

	stats := activityStatsForThread(t, svc, "thread-stats-lsp")
	if stats.LSPCalls != 2 {
		t.Fatalf("stats.LSPCalls = %d, want 2", stats.LSPCalls)
	}
	if got := stats.ToolCalls["lsp_edit"]; got != 2 {
		t.Fatalf("stats.ToolCalls[lsp_edit] = %d, want 2", got)
	}
}

func TestThreadPatch_TimelineDelta_NotFullTimeline(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.state.Threads = []ThreadSummary{{ID: "thread-patch", State: "running"}}

	svc.timeline.Append("thread-patch", "agent-1", timeline.Item{ID: "item-1", Kind: "tool", CallID: "call-1", Text: "first"})
	svc.timeline.Append("thread-patch", "agent-1", timeline.Item{ID: "item-2", Kind: "command", CallID: "call-2", Text: "second"})

	svc.mu.Lock()
	_ = svc.threadPatchLocked("thread-patch", "baseline")
	svc.mu.Unlock()

	svc.timeline.Append("thread-patch", "agent-1", timeline.Item{ID: "item-3", Kind: "file", CallID: "call-3", Text: "third"})

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-patch", "delta")
	svc.mu.Unlock()

	if got := len(patch.TimelineItems); got != 1 {
		t.Fatalf("len(patch.TimelineItems) = %d, want 1 delta item", got)
	}
	if got := patch.TimelineItems[0].ID; got != "item-3" {
		t.Fatalf("patch.TimelineItems[0].ID = %q, want %q", got, "item-3")
	}
	if len(patch.RemovedItemIds) != 0 {
		t.Fatalf("patch.RemovedItemIds = %#v, want empty", patch.RemovedItemIds)
	}
	if !reflect.DeepEqual(patch.TimelineOrder, []string{"item-1", "item-2", "item-3"}) {
		t.Fatalf("patch.TimelineOrder = %#v, want [item-1 item-2 item-3]", patch.TimelineOrder)
	}
}

func TestThreadPatch_ActivityStats_Included(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	setActivityStatsForThread(t, svc, "thread-stats-patch", 2, 1, 3, map[string]int64{"shell": 4, "lsp_edit": 3})
	svc.state.Threads = []ThreadSummary{{ID: "thread-stats-patch", State: "running"}}

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-stats-patch", "delta")
	svc.mu.Unlock()

	if patch.ActivityStats == nil {
		t.Fatal("patch.ActivityStats = nil, want non-nil")
	}
	if patch.ActivityStats.Commands != 2 || patch.ActivityStats.FileEdits != 1 || patch.ActivityStats.LSPCalls != 3 {
		t.Fatalf("patch.ActivityStats = %#v, want commands=2 fileEdits=1 lspCalls=3", patch.ActivityStats)
	}
	if got := patch.ActivityStats.ToolCalls["shell"]; got != 4 {
		t.Fatalf("patch.ActivityStats.ToolCalls[shell] = %d, want 4", got)
	}
}

func TestGetState_ActivityStatsByThread_Included(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	setActivityStatsForThread(t, svc, "thread-stats-state", 2, 1, 3, map[string]int64{"shell": 4})

	snapshot, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState() error = %v", err)
	}
	stats := snapshot.ActivityStatsByThread["thread-stats-state"]
	if stats == nil {
		t.Fatalf("ActivityStatsByThread[thread-stats-state] = %#v, want non-nil", snapshot.ActivityStatsByThread)
	}
	if stats.Commands != 2 || stats.FileEdits != 1 || stats.LSPCalls != 3 {
		t.Fatalf("snapshot.ActivityStatsByThread = %#v", snapshot.ActivityStatsByThread)
	}
	if got := stats.ToolCalls["shell"]; got != 4 {
		t.Fatalf("snapshot.ActivityStatsByThread[shell] = %d, want 4", got)
	}
}

func TestThreadPatch_TimelineDelta_RemovedAndOrdered(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	requirePhase2ActivityStats(t, svc)
	svc.state.Threads = []ThreadSummary{{ID: "thread-patch-evict", State: "running"}}
	svc.timeline = timeline.New(nil, nil, 2)

	svc.timeline.Append("thread-patch-evict", "agent-1", timeline.Item{ID: "item-1", Kind: "tool", CallID: "call-1", Text: "first"})
	svc.timeline.Append("thread-patch-evict", "agent-1", timeline.Item{ID: "item-2", Kind: "command", CallID: "call-2", Text: "second"})

	svc.mu.Lock()
	_ = svc.threadPatchLocked("thread-patch-evict", "baseline")
	svc.mu.Unlock()

	svc.timeline.Append("thread-patch-evict", "agent-1", timeline.Item{ID: "item-3", Kind: "file", CallID: "call-3", Text: "third"})

	svc.mu.Lock()
	patch := svc.threadPatchLocked("thread-patch-evict", "delta")
	svc.mu.Unlock()

	if !reflect.DeepEqual(patch.RemovedItemIds, []string{"item-1"}) {
		t.Fatalf("patch.RemovedItemIds = %#v, want [item-1]", patch.RemovedItemIds)
	}
	if !reflect.DeepEqual(patch.TimelineOrder, []string{"item-2", "item-3"}) {
		t.Fatalf("patch.TimelineOrder = %#v, want [item-2 item-3]", patch.TimelineOrder)
	}
	if got := len(patch.TimelineItems); got != 1 || patch.TimelineItems[0].ID != "item-3" {
		t.Fatalf("patch.TimelineItems = %#v, want only item-3 delta", patch.TimelineItems)
	}
}

func requirePhase2ActivityStats(t *testing.T, svc *service) {
	t.Helper()
	if svc == nil {
		t.Fatal("service = nil")
	}
	if svc.state.ActivityStatsByThread == nil {
		svc.state.ActivityStatsByThread = map[string]*ActivityStats{}
	}
}

func activityStatsForThread(t *testing.T, svc *service, threadID string) *ActivityStats {
	t.Helper()
	requirePhase2ActivityStats(t, svc)

	stats := svc.state.ActivityStatsByThread[threadID]
	if stats == nil {
		t.Fatalf("ActivityStatsByThread[%q] missing", threadID)
	}
	return stats
}

func setActivityStatsForThread(t *testing.T, svc *service, threadID string, commands, fileEdits, lspCalls int64, toolCalls map[string]int64) {
	t.Helper()
	requirePhase2ActivityStats(t, svc)

	svc.state.ActivityStatsByThread[threadID] = &ActivityStats{
		Commands:  commands,
		FileEdits: fileEdits,
		LSPCalls:  lspCalls,
		ToolCalls: cloneInt64Map(toolCalls),
	}
}
