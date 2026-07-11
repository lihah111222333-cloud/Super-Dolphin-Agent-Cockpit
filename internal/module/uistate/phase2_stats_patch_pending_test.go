package uistate

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kelindar/event"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate/timeline"
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

func TestActivityStats_UnknownItemFallsBackToCommandCount(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-unknown", "agent-1"), "turn-1")

	svc.applyItemStarted(turndto.ItemStarted{TurnHeader: turnHeader, ItemType: "request_user_input", CallID: "unknown-1"})

	stats := activityStatsForThread(t, svc, "thread-stats-unknown")
	if stats.Commands != 1 {
		t.Fatalf("stats.Commands = %d, want 1", stats.Commands)
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
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-lsp-1", ToolName: "patch_edit"},
	})
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-lsp-2", ToolName: "patch_edit"},
	})

	stats := activityStatsForThread(t, svc, "thread-stats-lsp")
	if stats.LSPCalls != 2 {
		t.Fatalf("stats.LSPCalls = %d, want 2", stats.LSPCalls)
	}
	if got := stats.ToolCalls["patch_edit"]; got != 2 {
		t.Fatalf("stats.ToolCalls[patch_edit] = %d, want 2", got)
	}
}

func TestActivityStats_MCPNamespacedLSPToolIncrementsLSPCalls(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-mcp-lsp", "agent-1"), "turn-1")

	// Runtime-emitted ToolName is the full MCP method (mcp__<server>__<name>).
	// LSPCalls must still recognize it as an LSP call after prefix stripping.
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-mcp-lsp-1", ToolName: "mcp__lsp__grep"},
	})
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-mcp-lsp-2", ToolName: "mcp__lsp__xref"},
	})

	stats := activityStatsForThread(t, svc, "thread-stats-mcp-lsp")
	if stats.LSPCalls != 2 {
		t.Fatalf("stats.LSPCalls = %d, want 2 (MCP-namespaced LSP tools should still count)", stats.LSPCalls)
	}
	// ToolCalls map preserves the original ev.ToolName key as the runtime sent it.
	if got := stats.ToolCalls["mcp__lsp__grep"]; got != 1 {
		t.Fatalf("stats.ToolCalls[mcp__lsp__grep] = %d, want 1", got)
	}
	if got := stats.ToolCalls["mcp__lsp__xref"]; got != 1 {
		t.Fatalf("stats.ToolCalls[mcp__lsp__xref] = %d, want 1", got)
	}
}

func TestActivityStats_CompletionIncrementsLSPCalls(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-completion", "agent-1"), "turn-1")

	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-completion-1", ToolName: "completion"},
	})
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-completion-2", ToolName: "mcp__lsp__completion"},
	})

	stats := activityStatsForThread(t, svc, "thread-stats-completion")
	if stats.LSPCalls != 2 {
		t.Fatalf("stats.LSPCalls = %d, want 2 for completion aliases", stats.LSPCalls)
	}
	if got := stats.ToolCalls["completion"]; got != 1 {
		t.Fatalf("stats.ToolCalls[completion] = %d, want 1", got)
	}
	if got := stats.ToolCalls["mcp__lsp__completion"]; got != 1 {
		t.Fatalf("stats.ToolCalls[mcp__lsp__completion] = %d, want 1", got)
	}
}

func TestCodexSyntheticLSPToolCallPublishesTimelineAndStatsPatch(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	t.Cleanup(func() { _ = dispatcher.Close() })
	svc := newProjectionTestService(t)
	svc.state.Threads = []ThreadSummary{{ID: "agent-1", AgentID: "agent-1", State: "running"}}
	svc.state.Agents = []AgentSummary{{ID: "agent-1", ThreadID: "agent-1", Provider: "codex", State: "running"}}
	svc.bindDispatcher(dispatcher)
	cancels := registerProjectionSubscriptions(dispatcher, svc)
	t.Cleanup(func() {
		for _, cancel := range cancels {
			if cancel != nil {
				cancel()
			}
		}
	})
	patches := make(chan uidto.UIThreadPatch, 4)
	cancelPatch := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { patches <- ev })
	t.Cleanup(cancelPatch)

	turnHeader := testTurnHeader(testAgentSessionHeader("agent-1", "agent-1"), "turn-1")
	event.Publish(dispatcher, tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-file-1", ToolName: "file"},
	})
	event.Publish(dispatcher, tooldto.ToolCallEnd{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-file-1", ToolName: "file"},
		Success:        true,
		Result:         `{"success":true,"path":"smoke.go"}`,
		ElapsedMS:      12,
	})

	evidence := codexLSPToolPatchEvidence{}
	assertEventually(t, time.Second, func() bool {
		return evidence.receive(patches)
	}, "codex synthetic LSP tool call should publish tool timeline and activity stats patch")
}

func TestActivityStats_MCPNamespacedNonLSPToolDoesNotIncrementLSPCalls(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	turnHeader := testTurnHeader(testAgentSessionHeader("thread-stats-mcp-other", "agent-1"), "turn-1")

	// Non-LSP MCP tool: stripped name is not in the current LSP tool set, so LSPCalls stays 0.
	svc.applyToolCallBegin(tooldto.ToolCallBegin{
		ToolCallHeader: sharedto.ToolCallHeader{TurnHeader: turnHeader, CallID: "call-mcp-other-1", ToolName: "mcp__json_render__render"},
	})

	stats := activityStatsForThread(t, svc, "thread-stats-mcp-other")
	if stats.LSPCalls != 0 {
		t.Fatalf("stats.LSPCalls = %d, want 0 (json_render is not LSP)", stats.LSPCalls)
	}
	if got := stats.ToolCalls["mcp__json_render__render"]; got != 1 {
		t.Fatalf("stats.ToolCalls[mcp__json_render__render] = %d, want 1", got)
	}
}

func TestNormalizeToolName(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"whitespace", "   \t\n", ""},
		{"short bare name", "grep", "grep"},
		{"mcp lsp namespaced", "mcp__lsp__grep", "grep"},
		{"completion", "completion", "completion"},
		{"mcp completion", "mcp__lsp__completion", "completion"},
		{"mcp orch namespaced", "mcp__orch__launch_agent", "launch_agent"},
		{"mcp playwright namespaced", "mcp__playwright__browser_click", "browser_click"},
		{"mcp prefix without server", "mcp__", "mcp__"},
		{"mcp prefix only", "mcp__lsp", "mcp__lsp"},
		{"non-mcp keeps name", "shell_exec", "shell_exec"},
		{"trims and lowercases", "  MCP__LSP__xref  ", "xref"},
		{"legacy bare name remains literal", "lsp_grep", "lsp_grep"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := normalizeToolName(tc.in); got != tc.want {
				t.Fatalf("normalizeToolName(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestClassifyToolActivity_HandlesMCPNamespace(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"bare collab tool", "launch_agent", "collab"},
		{"mcp namespaced collab tool", "mcp__orch__launch_agent", "collab"},
		{"mcp namespaced collab tool uppercase", "MCP__ORCH__SPAWN_AGENT", "collab"},
		{"mcp namespaced regular tool", "mcp__lsp__grep", "tool"},
		{"non-collab bare tool", "shell_exec", "tool"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := classifyToolActivity(tc.in); got != tc.want {
				t.Fatalf("classifyToolActivity(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
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
	setActivityStatsForThread(t, svc, "thread-stats-patch", 2, 1, 3, map[string]int64{"shell": 4, "patch_edit": 3})
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

type codexLSPToolPatchEvidence struct {
	sawStats    bool
	sawTimeline bool
}

func (e *codexLSPToolPatchEvidence) receive(patches <-chan uidto.UIThreadPatch) bool {
	for {
		select {
		case patch := <-patches:
			if codexLSPStatsPatchMatches(patch) {
				e.sawStats = true
			}
			if codexLSPTimelinePatchMatches(patch) {
				e.sawTimeline = true
			}
		default:
			return e.sawStats && e.sawTimeline
		}
	}
}

func codexLSPStatsPatchMatches(patch uidto.UIThreadPatch) bool {
	if patch.ThreadID != "agent-1" || patch.ActivityStats == nil {
		return false
	}
	return patch.ActivityStats.ToolCalls["file"] == 1 && patch.ActivityStats.LSPCalls == 1
}

func codexLSPTimelinePatchMatches(patch uidto.UIThreadPatch) bool {
	if patch.ThreadID != "agent-1" {
		return false
	}
	for _, item := range patch.TimelineItems {
		if item.Kind == "tool" && item.Tool == "file" {
			return true
		}
	}
	return false
}

func assertEventually(t *testing.T, timeout time.Duration, condition func() bool, message string) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}
