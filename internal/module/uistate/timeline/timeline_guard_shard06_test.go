package timeline_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kelindar/event"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/uistate/timeline"
)

func TestRegisterSubscriptions_ToolCallBeginAndEnd(t *testing.T) {
	svc, dispatcher, _ := newTimelineSubscriptionFixture(t, 0)

	event.Publish(dispatcher, toolCallBeginEvent("call-1", "bash", 42))
	waitForCondition(t, func() bool {
		return timelineHasItemKind(svc, "t1", 0, "tool")
	}, "expected one timeline item after tool call begin")
	assertToolBeginItem(t, svc.GetByThread("t1")[0], "call-1", "bash", 42)

	event.Publish(dispatcher, toolCallEndWithResultEvent("call-1", "bash", strings.Repeat("x", 210), 123))
	waitForCondition(t, func() bool {
		return hasCompletedToolItem(svc.GetByThread("t1"))
	}, "expected tool call item to be updated after tool call end")
	assertCompletedToolItem(t, svc.GetByThread("t1")[0], 123, 200)
}

func TestRegisterSubscriptions_ApprovalRequestAndResolve(t *testing.T) {
	svc, dispatcher, _ := newTimelineSubscriptionFixture(t, 0)

	event.Publish(dispatcher, approvalRequestedEvent("call-2", "file_edit", "", "tool"))
	waitForCondition(t, func() bool {
		return timelineHasItemKind(svc, "t1", 0, "approval")
	}, "expected one timeline item after approval request")
	assertApprovalPendingItem(t, svc.GetByThread("t1")[0])

	event.Publish(dispatcher, approvalResolvedEvent("call-2", "file_edit", "", true))
	waitForCondition(t, func() bool {
		return hasApprovedApprovalItem(svc.GetByThread("t1"))
	}, "expected approval request item to be updated after approval resolved")
	assertApprovalDoneItem(t, svc.GetByThread("t1")[0])
}

func TestRegisterSubscriptions_ApprovalIdentitySeparatesSameRequestID(t *testing.T) {
	svc, dispatcher, _ := newTimelineSubscriptionFixture(t, 0)

	first := approvalRequestedEvent("call-a", "file_edit", "", "tool")
	first.SessionScope = "session-a"
	first.RequestID = 7
	second := approvalRequestedEvent("call-b", "file_edit", "", "tool")
	second.SessionScope = "session-b"
	second.RequestID = 7
	event.Publish(dispatcher, first)
	event.Publish(dispatcher, second)
	waitForCondition(t, func() bool {
		return timelineHasItemCount(svc, "t1", 2)
	}, "expected approvals with the same request ID to remain distinct")

	resolved := approvalResolvedEvent("call-b", "file_edit", "", true)
	resolved.SessionScope = "session-b"
	resolved.RequestID = 7
	event.Publish(dispatcher, resolved)
	waitForCondition(t, func() bool {
		return timelineHasApprovalState(svc.GetByThread("t1"), "session-b", "call-b", 7, "approved", true)
	}, "expected resolution to update only its exact approval identity")

	items := svc.GetByThread("t1")
	if !timelineHasApprovalState(items, "session-a", "call-a", 7, "pending", false) {
		t.Fatalf("unrelated approval = %+v, want exact identity to remain pending", items)
	}
}

func TestRegisterSubscriptions_IncompleteResolutionDoesNotMutatePending(t *testing.T) {
	svc, dispatcher, _ := newTimelineSubscriptionFixture(t, 0)

	pending := approvalRequestedEvent("call-ambiguous", "file_edit", "", "tool")
	pending.SessionScope = "session-a"
	pending.RequestID = 9
	event.Publish(dispatcher, pending)
	waitForCondition(t, func() bool {
		return timelineHasItemCount(svc, "t1", 1)
	}, "expected canonical pending approval")

	resolved := approvalResolvedEvent("call-ambiguous", "file_edit", "", true)
	resolved.SessionScope = ""
	resolved.RequestID = 9
	event.Publish(dispatcher, resolved)
	waitForCondition(t, func() bool {
		return timelineHasItemCount(svc, "t1", 2)
	}, "expected incomplete resolution to append a display-only terminal item")

	items := svc.GetByThread("t1")
	if items[0].Status != "pending" || items[0].Done {
		t.Fatalf("canonical pending item = %+v, want untouched pending", items[0])
	}
	if items[1].Status != "approved" || !items[1].Done || items[1].SessionScope != "" {
		t.Fatalf("display-only terminal item = %+v, want approved with incomplete identity", items[1])
	}
}

func TestRegisterSubscriptions_ApprovalResolveFallbackPreservesRequestID(t *testing.T) {
	tests := []struct {
		name      string
		requestID int64
	}{
		{name: "canonical request identity", requestID: 41},
		{name: "display only terminal", requestID: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, dispatcher, _ := newTimelineSubscriptionFixture(t, 0)
			ev := approvalResolvedEvent("call-fallback", "file_edit", "", true)
			if tt.requestID > 0 {
				if err := json.Unmarshal([]byte(`{"request_id":41}`), &ev); err != nil {
					t.Fatalf("unmarshal resolved approval request identity: %v", err)
				}
			} else {
				ev.SessionScope = ""
				ev.RequestID = 0
			}

			event.Publish(dispatcher, ev)
			waitForCondition(t, func() bool {
				return hasApprovedApprovalItem(svc.GetByThread("t1"))
			}, "expected terminal approval fallback item")
			item := svc.GetByThread("t1")[0]
			if item.RequestID != tt.requestID {
				t.Fatalf("fallback RequestID = %d, want %d", item.RequestID, tt.requestID)
			}
		})
	}
}

func TestRegisterSubscriptions_ToolAndApprovalShareCallID(t *testing.T) {
	svc, dispatcher, updated := newTimelineSubscriptionFixture(t, 2)

	event.Publish(dispatcher, toolCallBeginEvent("call-1", "bash", 42))
	event.Publish(dispatcher, approvalRequestedEvent("call-1", "bash", "approval-1", "request_user_input"))

	waitForCondition(t, func() bool {
		return timelineHasItemCount(svc, "t1", 2)
	}, "expected tool call and approval request items to coexist")
	assertToolAndApprovalInitialItems(t, svc.GetByThread("t1"), "call-1")

	event.Publish(dispatcher, approvalResolvedEvent("call-1", "bash", "approval-1", true))
	event.Publish(dispatcher, toolCallEndEvent("call-1", "bash", true))

	waitForCondition(t, func() bool {
		return toolAndApprovalCompleted(svc.GetByThread("t1"))
	}, "expected tool and approval items to update independently")
	expectThreadUpdates(t, updated, 2, "t1")
}

func TestRegisterSubscriptions_ItemStartedAndCompleted(t *testing.T) {
	svc, dispatcher, _ := newTimelineSubscriptionFixture(t, 0)

	event.Publish(dispatcher, itemStartedEvent("command", "ls -la", "call-3"))
	waitForCondition(t, func() bool {
		return timelineHasItemKind(svc, "t1", 0, "command")
	}, "expected one timeline item after item started")
	assertCommandItemStarted(t, svc.GetByThread("t1")[0])

	event.Publish(dispatcher, itemCompletedEvent("call-3", true))
	waitForCondition(t, func() bool {
		return hasCompletedCommandItem(svc.GetByThread("t1"))
	}, "expected item to be updated after item completed")
	assertCommandItemCompleted(t, svc.GetByThread("t1")[0])
}

func TestRegisterSubscriptions_TimelineChangesNotifyOnUpdated(t *testing.T) {
	svc, dispatcher, updated := newTimelineSubscriptionFixture(t, 6)

	event.Publish(dispatcher, itemCompletedEvent("missing-item", true))
	assertNoThreadUpdate(t, updated)

	publishItemUpdatePair(t, svc, dispatcher)
	publishToolUpdatePair(t, svc, dispatcher)
	publishApprovalUpdatePair(t, svc, dispatcher)
	expectThreadUpdates(t, updated, 6, "t1")
}

func newTimelineSubscriptionFixture(t *testing.T, updateBuffer int) (timeline.Service, *event.Dispatcher, chan string) {
	t.Helper()

	var updated chan string
	var onUpdated func(string)
	if updateBuffer > 0 {
		updated = make(chan string, updateBuffer)
		onUpdated = func(threadID string) { updated <- threadID }
	}
	svc := timeline.New(nil, nil, 50)
	dispatcher := event.NewDispatcher()
	cancels := timeline.RegisterSubscriptions(dispatcher, svc, nil, onUpdated)
	t.Cleanup(func() { cancelTimelineSubscriptions(cancels) })
	return svc, dispatcher, updated
}

func cancelTimelineSubscriptions(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		cancel()
	}
}

func turnHeader() shared.TurnHeader {
	return shared.TurnHeader{
		AgentHeader: shared.AgentHeader{
			ThreadHeader: shared.ThreadHeader{ThreadID: "t1"},
			AgentID:      "agent-1",
		},
		TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
	}
}

func toolCallHeader(callID, toolName string) shared.ToolCallHeader {
	return shared.ToolCallHeader{
		TurnHeader: turnHeader(),
		CallID:     callID,
		ToolName:   toolName,
	}
}

func toolCallBeginEvent(callID, toolName string, requestID int64) tooldto.ToolCallBegin {
	return tooldto.ToolCallBegin{
		ToolCallHeader: toolCallHeader(callID, toolName),
		RequestID:      requestID,
	}
}

func toolCallEndEvent(callID, toolName string, success bool) tooldto.ToolCallEnd {
	return tooldto.ToolCallEnd{
		ToolCallHeader: toolCallHeader(callID, toolName),
		Success:        success,
	}
}

func toolCallEndWithResultEvent(callID, toolName, result string, elapsedMS int) tooldto.ToolCallEnd {
	ev := toolCallEndEvent(callID, toolName, true)
	ev.Result = result
	ev.ElapsedMS = int64(elapsedMS)
	return ev
}

func approvalRequestedEvent(callID, toolName, approvalID, kind string) tooldto.ToolApprovalRequested {
	return tooldto.ToolApprovalRequested{
		ToolApprovalHeader: shared.ToolApprovalHeader{
			ToolCallHeader: toolCallHeader(callID, toolName),
			SessionScope:   "test-session-scope",
			ApprovalID:     approvalID,
		},
		RequestID: 1,
		Kind:      kind,
	}
}

func approvalResolvedEvent(callID, toolName, approvalID string, approved bool) tooldto.ToolApprovalResolved {
	return tooldto.ToolApprovalResolved{
		ToolApprovalHeader: shared.ToolApprovalHeader{
			ToolCallHeader: toolCallHeader(callID, toolName),
			SessionScope:   "test-session-scope",
			ApprovalID:     approvalID,
		},
		RequestID: 1,
		Approved:  approved,
	}
}

func itemStartedEvent(itemType, command, callID string) turndto.ItemStarted {
	return turndto.ItemStarted{
		TurnHeader: turnHeader(),
		ItemType:   itemType,
		Command:    command,
		CallID:     callID,
	}
}

func itemCompletedEvent(callID string, success bool) turndto.ItemCompleted {
	return turndto.ItemCompleted{
		TurnHeader: turnHeader(),
		CallID:     callID,
		Success:    success,
	}
}

func timelineHasItemKind(svc timeline.Service, threadID string, index int, kind string) bool {
	item, ok := timelineItemAt(svc, threadID, index)
	return ok && item.Kind == kind
}

func timelineHasItemCount(svc timeline.Service, threadID string, want int) bool {
	return len(svc.GetByThread(threadID)) == want
}

func timelineHasApprovalState(items []timeline.Item, sessionScope, callID string, requestID int64, status string, done bool) bool {
	for _, item := range items {
		if item.SessionScope != sessionScope {
			continue
		}
		if item.CallID != callID {
			continue
		}
		if item.RequestID != requestID {
			continue
		}
		return item.Status == status && item.Done == done
	}
	return false
}

func timelineItemAt(svc timeline.Service, threadID string, index int) (timeline.Item, bool) {
	items := svc.GetByThread(threadID)
	if len(items) <= index {
		return timeline.Item{}, false
	}
	return items[index], true
}

func assertToolBeginItem(t *testing.T, item timeline.Item, callID, toolName string, requestID int64) {
	t.Helper()

	if item.Kind != "tool" {
		t.Fatalf("item.Kind = %q, want tool", item.Kind)
	}
	if item.Tool != toolName || item.ToolName != toolName {
		t.Fatalf("tool fields = %q/%q, want %q", item.Tool, item.ToolName, toolName)
	}
	if item.CallID != callID {
		t.Fatalf("item.CallID = %q, want %q", item.CallID, callID)
	}
	if item.RequestID != requestID {
		t.Fatalf("item.RequestID = %d, want %d", item.RequestID, requestID)
	}
}

func hasCompletedToolItem(items []timeline.Item) bool {
	if len(items) != 1 {
		return false
	}
	item := items[0]
	return item.Status == "completed" && item.Done && item.ElapsedMS != nil && item.Preview != ""
}

func assertCompletedToolItem(t *testing.T, item timeline.Item, elapsedMS int, previewRunes int) {
	t.Helper()

	if item.Status != "completed" {
		t.Fatalf("item.Status = %q, want completed", item.Status)
	}
	if item.Success == nil || !*item.Success {
		t.Fatalf("item.Success = %v, want true", item.Success)
	}
	if !item.Done {
		t.Fatal("item.Done = false, want true")
	}
	if item.ElapsedMS == nil || *item.ElapsedMS != elapsedMS {
		t.Fatalf("item.ElapsedMS = %v, want %d", item.ElapsedMS, elapsedMS)
	}
	if got := len([]rune(item.Preview)); got != previewRunes {
		t.Fatalf("len(item.Preview) = %d, want %d", got, previewRunes)
	}
}

func assertApprovalPendingItem(t *testing.T, item timeline.Item) {
	t.Helper()

	if item.Kind != "approval" {
		t.Fatalf("item.Kind = %q, want approval", item.Kind)
	}
	if item.Status != "pending" {
		t.Fatalf("item.Status = %q, want pending", item.Status)
	}
}

func hasApprovedApprovalItem(items []timeline.Item) bool {
	if len(items) != 1 {
		return false
	}
	return items[0].Status == "approved" && items[0].Done
}

func assertApprovalDoneItem(t *testing.T, item timeline.Item) {
	t.Helper()

	if item.Status != "approved" {
		t.Fatalf("item.Status = %q, want approved", item.Status)
	}
	if !item.Done {
		t.Fatal("item.Done = false, want true")
	}
}

func assertToolAndApprovalInitialItems(t *testing.T, items []timeline.Item, callID string) {
	t.Helper()

	toolItem := findTimelineItemByKind(items, "tool")
	approvalItem := findTimelineItemByKind(items, "approval")
	if toolItem == nil {
		t.Fatalf("items = %#v, want tool entry", items)
	}
	if approvalItem == nil {
		t.Fatalf("items = %#v, want approval entry", items)
	}
	if toolItem.CallID != callID || approvalItem.CallID != callID {
		t.Fatalf("call IDs = %q/%q, want %q", toolItem.CallID, approvalItem.CallID, callID)
	}
}

func findTimelineItemByKind(items []timeline.Item, kind string) *timeline.Item {
	for i := range items {
		if items[i].Kind == kind {
			return &items[i]
		}
	}
	return nil
}

func toolAndApprovalCompleted(items []timeline.Item) bool {
	if len(items) != 2 {
		return false
	}
	return completedToolStatus(findTimelineItemByKind(items, "tool")) &&
		approvedApprovalStatus(findTimelineItemByKind(items, "approval"))
}

func completedToolStatus(item *timeline.Item) bool {
	return item != nil && item.Status == "completed" && item.Success != nil && *item.Success
}

func approvedApprovalStatus(item *timeline.Item) bool {
	return item != nil && item.Status == "approved" && item.Done
}

func assertCommandItemStarted(t *testing.T, item timeline.Item) {
	t.Helper()

	if item.Kind != "command" {
		t.Fatalf("item.Kind = %q, want command", item.Kind)
	}
	if item.ItemType != "command" {
		t.Fatalf("item.ItemType = %q, want command", item.ItemType)
	}
}

func hasCompletedCommandItem(items []timeline.Item) bool {
	if len(items) != 1 {
		return false
	}
	item := items[0]
	return item.Status == "completed" && item.Done && item.Success != nil && *item.Success
}

func assertCommandItemCompleted(t *testing.T, item timeline.Item) {
	t.Helper()

	if item.Status != "completed" {
		t.Fatalf("item.Status = %q, want completed", item.Status)
	}
	if item.Success == nil || !*item.Success {
		t.Fatalf("item.Success = %v, want true", item.Success)
	}
	if !item.Done {
		t.Fatal("item.Done = false, want true")
	}
}

func publishItemUpdatePair(t *testing.T, svc timeline.Service, dispatcher *event.Dispatcher) {
	t.Helper()

	event.Publish(dispatcher, itemStartedEvent("command", "pwd", "call-item"))
	waitForCondition(t, func() bool {
		return timelineHasCallID(svc, "t1", 0, "call-item")
	}, "expected item start before item completed callback")
	event.Publish(dispatcher, itemCompletedEvent("call-item", true))
}

func publishToolUpdatePair(t *testing.T, svc timeline.Service, dispatcher *event.Dispatcher) {
	t.Helper()

	event.Publish(dispatcher, toolCallBeginEvent("call-tool", "bash", 0))
	waitForCondition(t, func() bool {
		return timelineHasCallID(svc, "t1", 1, "call-tool")
	}, "expected tool call begin before tool call end callback")
	event.Publish(dispatcher, toolCallEndEvent("call-tool", "bash", true))
}

func publishApprovalUpdatePair(t *testing.T, svc timeline.Service, dispatcher *event.Dispatcher) {
	t.Helper()

	event.Publish(dispatcher, approvalRequestedEvent("call-approval", "file_edit", "", "tool"))
	waitForCondition(t, func() bool {
		return timelineHasCallID(svc, "t1", 2, "call-approval")
	}, "expected approval request before approval resolved callback")
	event.Publish(dispatcher, approvalResolvedEvent("call-approval", "file_edit", "", true))
}

func timelineHasCallID(svc timeline.Service, threadID string, index int, callID string) bool {
	item, ok := timelineItemAt(svc, threadID, index)
	return ok && item.CallID == callID
}

func expectThreadUpdates(t *testing.T, updated <-chan string, count int, want string) {
	t.Helper()

	for i := range count {
		if threadID := mustReceiveThreadUpdate(t, updated); threadID != want {
			t.Fatalf("updated[%d] = %q, want %q", i, threadID, want)
		}
	}
}
