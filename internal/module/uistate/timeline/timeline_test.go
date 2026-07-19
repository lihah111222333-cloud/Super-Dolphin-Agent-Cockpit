package timeline_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/kelindar/event"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate/timeline"
)

func TestAppendAndGetByThread(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	item := timeline.Item{
		ID:     "item-1",
		Kind:   "tool",
		CallID: "call-abc",
		Status: "running",
	}
	svc.Append("t1", "agent-1", item)

	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ID != "item-1" {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, "item-1")
	}
	if items[0].CallID != "call-abc" {
		t.Fatalf("items[0].CallID = %q, want %q", items[0].CallID, "call-abc")
	}
}

func TestAppendRespectsCapacity(t *testing.T) {
	svc := timeline.New(nil, nil, 3)
	for i := range 5 {
		svc.Append("t1", "a", timeline.Item{
			ID:   fmt.Sprintf("item-%d", i),
			Kind: "msg",
		})
	}
	items := svc.GetByThread("t1")
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	if items[0].ID != "item-2" {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, "item-2")
	}
}

func TestUpdateByCallID(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{
		ID: "item-1", Kind: "tool", CallID: "call-1", Status: "running",
	})

	updated := svc.UpdateByCallID("t1", "a", "call-1", func(item *timeline.Item) {
		item.Status = "completed"
		b := true
		item.Success = &b
	})
	if !updated {
		t.Fatal("updated = false, want true")
	}

	items := svc.GetByThread("t1")
	if items[0].Status != "completed" {
		t.Fatalf("items[0].Status = %q, want %q", items[0].Status, "completed")
	}
}

func TestUpdateByCallIDDoesNotEmit(t *testing.T) {
	emitted := 0
	emitter := func(uidto.UITimelineAppended) {
		emitted++
	}
	svc := timeline.New(nil, emitter, 50)
	svc.Append("t1", "a", timeline.Item{
		ID: "i1", Kind: "tool", CallID: "c1", Status: "running",
	})
	emitted = 0

	updated := svc.UpdateByCallID("t1", "a", "c1", func(it *timeline.Item) {
		it.Status = "completed"
	})
	if !updated {
		t.Fatal("updated = false, want true")
	}
	if emitted != 0 {
		t.Fatalf("UpdateByCallID should not emit, got %d emissions", emitted)
	}
}

func TestAppendDedup_SameCallID(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{
		ID: "item-1", Kind: "tool", CallID: "call-1", Status: "running",
	})
	svc.Append("t1", "a", timeline.Item{
		ID: "item-1-dup", Kind: "tool", CallID: "call-1", Status: "running",
	})
	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].ID != "item-1" {
		t.Fatalf("items[0].ID = %q, want %q", items[0].ID, "item-1")
	}
}

func TestAppendDedup_MergeKeepsCompletedStatus(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	elapsed := 123
	svc.Append("t1", "a", timeline.Item{
		ID:        "item-1",
		Kind:      "tool",
		CallID:    "call-1",
		Status:    "completed",
		Tool:      "bash",
		Done:      true,
		Preview:   "before",
		RequestID: 7,
	})
	svc.Append("t1", "a", timeline.Item{
		ID:        "item-1-dup",
		Kind:      "tool",
		CallID:    "call-1",
		Status:    "running",
		Tool:      "bash",
		Preview:   "after",
		ElapsedMS: &elapsed,
	})

	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if got := items[0].Status; got != "completed" {
		t.Fatalf("items[0].Status = %q, want %q", got, "completed")
	}
	if got := items[0].ID; got != "item-1" {
		t.Fatalf("items[0].ID = %q, want %q", got, "item-1")
	}
	if got := items[0].Tool; got != "bash" {
		t.Fatalf("items[0].Tool = %q, want %q", got, "bash")
	}
	if got := items[0].Preview; got != "after" {
		t.Fatalf("items[0].Preview = %q, want %q", got, "after")
	}
	if items[0].ElapsedMS == nil || *items[0].ElapsedMS != 123 {
		t.Fatalf("items[0].ElapsedMS = %v, want 123", items[0].ElapsedMS)
	}
	if !items[0].Done {
		t.Fatal("items[0].Done = false, want true")
	}
}

func TestAppendDedup_SameTurnKind(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{ID: "ts-1", Kind: "turn_start", TurnID: "turn-1"})
	svc.Append("t1", "a", timeline.Item{ID: "ts-1-dup", Kind: "turn_start", TurnID: "turn-1"})
	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
}

func TestSnapshot(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{ID: "i1", Kind: "turn_start"})
	svc.Append("t2", "a", timeline.Item{ID: "i2", Kind: "turn_start"})
	snap := svc.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("len(snap) = %d, want 2", len(snap))
	}
	if len(snap["t1"]) != 1 {
		t.Fatalf("len(snap[t1]) = %d, want 1", len(snap["t1"]))
	}
	if len(snap["t2"]) != 1 {
		t.Fatalf("len(snap[t2]) = %d, want 1", len(snap["t2"]))
	}
}

func TestUpdateByCallID_NonExistentCallID(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	svc.Append("t1", "a", timeline.Item{ID: "i1", Kind: "tool", CallID: "c1"})

	updated := svc.UpdateByCallID("t1", "a", "non-existent", func(it *timeline.Item) {
		it.Status = "completed"
	})
	if updated {
		t.Fatal("updated = true, want false")
	}

	items := svc.GetByThread("t1")
	if items[0].Status != "" {
		t.Fatalf("items[0].Status = %q, want empty", items[0].Status)
	}
}

func TestAppendRespectsCapacity_IndexConsistency(t *testing.T) {
	svc := timeline.New(nil, nil, 3)
	for i := range 5 {
		svc.Append("t1", "a", timeline.Item{
			ID:     fmt.Sprintf("item-%d", i),
			Kind:   "tool",
			CallID: fmt.Sprintf("call-%d", i),
		})
	}

	items := svc.GetByThread("t1")
	if len(items) != 3 {
		t.Fatalf("len(items) = %d, want 3", len(items))
	}
	for _, it := range items {
		updated := svc.UpdateByCallID("t1", "a", it.CallID, func(item *timeline.Item) {
			item.Status = "checked"
		})
		if !updated {
			t.Fatalf("updated = false for %s, want true", it.CallID)
		}
	}
	if svc.UpdateByCallID("t1", "a", "call-0", func(it *timeline.Item) {}) {
		t.Fatal("updated evicted call-0, want false")
	}
}

func TestRegisterSubscriptions_TurnStarted(t *testing.T) {
	emitted := make(chan uidto.UITimelineAppended, 1)
	emitter := func(ev uidto.UITimelineAppended) {
		emitted <- ev
	}

	svc := timeline.New(nil, emitter, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	event.Publish(dispatcher, turndto.TurnStarted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
	})

	waitForCondition(t, func() bool {
		return len(svc.GetByThread("t1")) == 1
	}, "expected one timeline item after turn started")
	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Kind != "turn_start" {
		t.Fatalf("items[0].Kind = %q, want %q", items[0].Kind, "turn_start")
	}
	if items[0].TurnID != "turn-1" {
		t.Fatalf("items[0].TurnID = %q, want %q", items[0].TurnID, "turn-1")
	}
	if items[0].Status != "running" {
		t.Fatalf("items[0].Status = %q, want %q", items[0].Status, "running")
	}
	ev := mustReceiveTimelineAppended(t, emitted)
	if ev.Projection != "timeline" {
		t.Fatalf("emitted.Projection = %q, want %q", ev.Projection, "timeline")
	}
}

func TestRegisterSubscriptions_TurnCompleted(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	event.Publish(dispatcher, turndto.TurnStarted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
	})
	waitForCondition(t, func() bool {
		items := svc.GetByThread("t1")
		return len(items) == 1 && items[0].Kind == "turn_start"
	}, "expected turn start item before turn completed")
	event.Publish(dispatcher, canonicalTimelineTurnCompleted(t, turndto.TurnCompleted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{EventHeader: shared.EventHeader{Timestamp: time.Now().UTC()}, ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
		Success: true,
		Status:  "completed",
	}))

	time.Sleep(50 * time.Millisecond)
	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Kind != "turn_start" {
		t.Fatalf("items[0].Kind = %q, want %q", items[0].Kind, "turn_start")
	}
}

func TestRegisterSubscriptions_TurnInterrupted(t *testing.T) {
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, nil)
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()

	event.Publish(dispatcher, turndto.TurnInterrupted{
		TurnHeader: shared.TurnHeader{
			AgentHeader: shared.AgentHeader{
				ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
				AgentID:      "agent-1",
			},
			TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
		},
	})

	waitForCondition(t, func() bool {
		return len(svc.GetByThread("t1")) == 1
	}, "expected one timeline item after turn interrupted")
	items := svc.GetByThread("t1")
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Kind != "turn_interrupted" {
		t.Fatalf("items[0].Kind = %q, want %q", items[0].Kind, "turn_interrupted")
	}
	if items[0].Status != "interrupted" {
		t.Fatalf("items[0].Status = %q, want %q", items[0].Status, "interrupted")
	}
}

func waitForCondition(t *testing.T, fn func() bool, message string) {
	t.Helper()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if fn() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal(message)
}

func assertStableItemCount(t *testing.T, svc timeline.Service, threadID string, want int, message string) {
	t.Helper()

	deadline := time.Now().Add(150 * time.Millisecond)
	for time.Now().Before(deadline) {
		if got := len(svc.GetByThread(threadID)); got != want {
			t.Fatalf("%s: got %d items, want %d", message, got, want)
		}
		time.Sleep(15 * time.Millisecond)
	}
}

func mustReceiveTimelineAppended(t *testing.T, ch <-chan uidto.UITimelineAppended) uidto.UITimelineAppended {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(time.Second):
		t.Fatal("expected UITimelineAppended event")
		return uidto.UITimelineAppended{}
	}
}

func mustReceiveThreadUpdate(t *testing.T, ch <-chan string) string {
	t.Helper()

	select {
	case threadID := <-ch:
		return threadID
	case <-time.After(time.Second):
		t.Fatal("expected onUpdated callback")
		return ""
	}
}

func assertNoThreadUpdate(t *testing.T, ch <-chan string) {
	t.Helper()

	select {
	case threadID := <-ch:
		t.Fatalf("unexpected onUpdated callback for thread %q", threadID)
	case <-time.After(50 * time.Millisecond):
	}
}
