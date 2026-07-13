package timeline

import (
	"testing"
	"time"

	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
)

func TestApprovalTimelineSeparatesDelimiterCollidingIdentities(t *testing.T) {
	svc := New(nil, nil, 10)
	requested := approvalRequestedHandler(svc, nil)
	resolved := approvalResolvedHandler(svc, nil)
	first := approvalTimelineTestHeader("a:b", "c", "tool-a")
	second := approvalTimelineTestHeader("a", "b:c", "tool-b")

	requested(tooldto.ToolApprovalRequested{ToolApprovalHeader: first, RequestID: 1, Kind: "tool"})
	requested(tooldto.ToolApprovalRequested{ToolApprovalHeader: second, RequestID: 1, Kind: "tool"})

	items := svc.GetByThread("thread-1")
	if len(items) != 2 {
		t.Fatalf("approval rows = %d, want 2 independent rows: %+v", len(items), items)
	}
	firstItem := requireApprovalTimelineItem(t, items, "a:b", "c", 1)
	secondItem := requireApprovalTimelineItem(t, items, "a", "b:c", 1)
	requireApprovalTimelineIDs(t, firstItem, secondItem)

	resolved(tooldto.ToolApprovalResolved{ToolApprovalHeader: first, RequestID: 1, Approved: true})
	items = svc.GetByThread("thread-1")
	firstItem = requireApprovalTimelineItem(t, items, "a:b", "c", 1)
	secondItem = requireApprovalTimelineItem(t, items, "a", "b:c", 1)
	requireApprovalTimelineState(t, firstItem, "approved", true)
	requireApprovalTimelineState(t, secondItem, "pending", false)

	resolved(tooldto.ToolApprovalResolved{ToolApprovalHeader: second, RequestID: 1, Approved: false})
	items = svc.GetByThread("thread-1")
	firstItem = requireApprovalTimelineItem(t, items, "a:b", "c", 1)
	secondItem = requireApprovalTimelineItem(t, items, "a", "b:c", 1)
	requireApprovalTimelineState(t, firstItem, "approved", true)
	requireApprovalTimelineState(t, secondItem, "rejected", true)
}

func approvalTimelineTestHeader(sessionScope, callID, toolName string) shared.ToolApprovalHeader {
	return shared.ToolApprovalHeader{
		ToolCallHeader: shared.ToolCallHeader{
			TurnHeader: shared.TurnHeader{
				AgentHeader: shared.AgentHeader{
					ThreadHeader: shared.ThreadHeader{
						EventHeader: shared.EventHeader{Timestamp: time.Unix(1710000000, 0).UTC()},
						ThreadID:    "thread-1",
					},
					AgentID: "agent-1",
				},
				TurnIDHeader: shared.TurnIDHeader{TurnID: "turn-1"},
			},
			CallID:   callID,
			ToolName: toolName,
		},
		SessionScope: sessionScope,
	}
}

func requireApprovalTimelineItem(t *testing.T, items []Item, sessionScope, callID string, requestID int64) Item {
	t.Helper()
	for _, item := range items {
		if item.SessionScope == sessionScope && item.CallID == callID && item.RequestID == requestID {
			return item
		}
	}
	t.Fatalf("approval identity (%q, %q, %d) missing from %+v", sessionScope, callID, requestID, items)
	return Item{}
}

func requireApprovalTimelineIDs(t *testing.T, first, second Item) {
	t.Helper()
	if first.ID == second.ID {
		t.Fatalf("colliding approval item IDs = %q", first.ID)
	}
	if first.ID != approvalUpdateKey("a:b", "c", 1) || second.ID != approvalUpdateKey("a", "b:c", 1) {
		t.Fatalf("approval item IDs are not the canonical lookup keys: first=%q second=%q", first.ID, second.ID)
	}
}

func requireApprovalTimelineState(t *testing.T, item Item, status string, done bool) {
	t.Helper()
	if item.Status != status || item.Done != done {
		t.Fatalf("approval row = %+v, want status %q and done %v", item, status, done)
	}
}
