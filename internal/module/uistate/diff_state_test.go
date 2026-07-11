package uistate

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kelindar/event"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
)

func TestDiffStateByAgentSelectsActiveMainAgent(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.mu.Lock()
	seedDiffStateThread(svc, "thread-1", "agent-a", "agent-b")
	svc.state.ActiveThreadID = "thread-1"
	svc.state.MainAgentID = "agent-a"
	if !svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-a", 0) {
		t.Fatal("applyToolDiffUpdatedLocked(agent-a) = false, want true")
	}
	if !svc.applyToolDiffUpdatedLocked("agent-b", "thread-1", "diff-b", 0) {
		t.Fatal("applyToolDiffUpdatedLocked(agent-b) = false, want true")
	}
	gotA := svc.currentDiffTextLocked("thread-1")
	revA := svc.currentDiffRevisionLocked("thread-1")
	svc.state.MainAgentID = "agent-b"
	gotB := svc.currentDiffTextLocked("thread-1")
	revB := svc.currentDiffRevisionLocked("thread-1")
	stored := map[string]string{
		"agent-a": svc.state.DiffTextByAgent["agent-a"],
		"agent-b": svc.state.DiffTextByAgent["agent-b"],
	}
	svc.mu.Unlock()

	if gotA != "diff-a" || gotB != "diff-b" {
		t.Fatalf("current diff texts = (%q, %q), want (diff-a, diff-b)", gotA, gotB)
	}
	if revA != 1 || revB != 1 {
		t.Fatalf("current revisions = (%d, %d), want (1, 1)", revA, revB)
	}
	if !reflect.DeepEqual(stored, map[string]string{"agent-a": "diff-a", "agent-b": "diff-b"}) {
		t.Fatalf("stored diff map = %#v", stored)
	}
}

func TestGetStateDiffSnapshotHonorsKnownRevision(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.mu.Lock()
	seedDiffStateThread(svc, "thread-1", "agent-a")
	if !svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-1", 0) {
		t.Fatal("first applyToolDiffUpdatedLocked() = false, want true")
	}
	if svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-1", 0) {
		t.Fatal("second applyToolDiffUpdatedLocked() = true, want false for unchanged diff")
	}
	svc.mu.Unlock()

	full, err := svc.GetState(withDiffStateRequest(context.Background(), "thread-1", true, 0))
	if err != nil {
		t.Fatalf("GetState(full) error = %v", err)
	}
	assertDiffSnapshotChanged(t, full, "diff-1", 1)

	unchanged, err := svc.GetState(withDiffStateRequest(context.Background(), "thread-1", true, 1))
	if err != nil {
		t.Fatalf("GetState(unchanged) error = %v", err)
	}
	assertDiffSnapshotUnchanged(t, unchanged, 1)
}

func assertDiffSnapshotChanged(t *testing.T, state *UIState, text string, revision int64) {
	t.Helper()
	if state.Unchanged {
		t.Fatalf("full.Unchanged = true, want false")
	}
	if got := state.DiffTextByAgent["thread-1"]; got != text {
		t.Fatalf("full.DiffTextByAgent[thread-1] = %q, want %s", got, text)
	}
	if got := state.DiffRevisionByAgent["thread-1"]; got != revision {
		t.Fatalf("full.DiffRevisionByAgent[thread-1] = %d, want %d", got, revision)
	}
}

func assertDiffSnapshotUnchanged(t *testing.T, state *UIState, revision int64) {
	t.Helper()
	if !state.Unchanged {
		t.Fatalf("unchanged.Unchanged = false, want true")
	}
	if len(state.DiffTextByAgent) != 0 {
		t.Fatalf("unchanged.DiffTextByAgent = %#v, want empty map", state.DiffTextByAgent)
	}
	if got := state.DiffRevisionByAgent["thread-1"]; got != revision {
		t.Fatalf("unchanged.DiffRevisionByAgent[thread-1] = %d, want %d", got, revision)
	}
}

func TestApplyToolDiffUpdatedLockedUsesEventRevision(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.mu.Lock()
	seedDiffStateThread(svc, "thread-1", "agent-a")
	if !svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-1", 7) {
		t.Fatal("applyToolDiffUpdatedLocked(diff-1, rev=7) = false, want true")
	}
	if got := svc.state.DiffRevisionByAgent["agent-a"]; got != 7 {
		t.Fatalf("DiffRevisionByAgent[agent-a] = %d, want 7", got)
	}
	if svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-1", 7) {
		t.Fatal("applyToolDiffUpdatedLocked(same diff, same rev) = true, want false")
	}
	if !svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-1", 8) {
		t.Fatal("applyToolDiffUpdatedLocked(same diff, higher rev) = false, want true")
	}
	if got := svc.state.DiffRevisionByAgent["agent-a"]; got != 8 {
		t.Fatalf("DiffRevisionByAgent[agent-a] = %d, want 8", got)
	}
	if !svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-2", 1) {
		t.Fatal("applyToolDiffUpdatedLocked(diff-2, rev=1) = false, want true")
	}
	if got := svc.state.DiffRevisionByAgent["agent-a"]; got != 1 {
		t.Fatalf("DiffRevisionByAgent[agent-a] = %d, want 1 after session reset", got)
	}
	svc.mu.Unlock()
}

func TestGetStateDiffSnapshotReturnsExplicitEmptyDiffAfterClear(t *testing.T) {
	t.Parallel()

	svc := newProjectionTestService(t)
	svc.mu.Lock()
	seedDiffStateThread(svc, "thread-1", "agent-a")
	if !svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "diff-1", 0) {
		t.Fatal("applyToolDiffUpdatedLocked(diff-1) = false, want true")
	}
	if !svc.applyToolDiffUpdatedLocked("agent-a", "thread-1", "", 0) {
		t.Fatal("applyToolDiffUpdatedLocked(clear) = false, want true")
	}
	svc.mu.Unlock()

	cleared, err := svc.GetState(withDiffStateRequest(context.Background(), "thread-1", true, 2))
	if err != nil {
		t.Fatalf("GetState(cleared) error = %v", err)
	}
	if cleared.Unchanged {
		t.Fatalf("cleared.Unchanged = true, want false")
	}
	got, ok := cleared.DiffTextByAgent["thread-1"]
	if !ok || got != "" {
		t.Fatalf("cleared.DiffTextByAgent[thread-1] = (%q, %t), want (\"\", true)", got, ok)
	}
	if got := cleared.DiffRevisionByAgent["thread-1"]; got != 0 {
		t.Fatalf("cleared.DiffRevisionByAgent[thread-1] = %d, want 0", got)
	}
}

func TestProjectionSubscriptionsApplyToolDiffUpdatedPublishesPatch(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	svc := newProjectionTestService(t)
	svc.bindDispatcher(dispatcher)
	cancels := registerProjectionSubscriptions(dispatcher, svc)
	defer cancelAll(cancels)

	svc.mu.Lock()
	seedDiffStateThread(svc, "thread-1", "agent-1")
	svc.mu.Unlock()

	got := make(chan uidto.UIThreadPatch, 2)
	cancel := event.Subscribe(dispatcher, func(ev uidto.UIThreadPatch) { got <- ev })
	defer cancel()

	event.Publish(dispatcher, tooldto.ToolDiffUpdated{
		ThreadID: "thread-1",
		AgentID:  "agent-1",
		DiffText: "--- a/main.go\n+++ b/main.go\n@@ -1 +1 @@\n-old\n+new\n",
		Revision: 7,
	})

	patch := mustReceiveThreadPatch(t, got)
	if patch.ThreadID != "thread-1" || patch.Source != "tool/diffUpdated" {
		t.Fatalf("patch identity = %#v", patch)
	}
	if patch.DiffRevision != 7 || patch.DiffText == "" {
		t.Fatalf("patch diff = %#v", patch)
	}

	select {
	case duplicate := <-got:
		t.Fatalf("unexpected duplicate patch for unchanged diff: %#v", duplicate)
	case <-time.After(150 * time.Millisecond):
	}
}

func seedDiffStateThread(svc *service, threadID string, agentIDs ...string) {
	svc.state.Threads = []ThreadSummary{{ID: threadID, AgentID: agentIDs[0]}}
	svc.state.Agents = svc.state.Agents[:0]
	for _, agentID := range agentIDs {
		svc.state.Agents = append(svc.state.Agents, AgentSummary{ID: agentID, ThreadID: threadID})
	}
}
