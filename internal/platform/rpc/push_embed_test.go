package rpc

import (
	"context"
	"testing"
	"time"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	uidto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/ui"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/eventsurface"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestRPCPushWorkerDoesNotEmbedThreadPatchIntoReasoningOrStdoutDelta(t *testing.T) {
	tests := []struct {
		name   string
		method string
	}{
		{name: "reasoning delta", method: eventsurface.MethodReasoningTextDelta},
		{name: "command output delta", method: eventsurface.MethodCommandOutputDelta},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			broadcaster := &fakePushBroadcaster{}
			bridge := &PushBridge{logger: pkglogger.Get()}
			worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
			worker.Enqueue([]eventsurface.Notification{
				{Method: tc.method, Payload: map[string]any{"threadId": "thread-1", "delta": "not message stream"}},
				{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{
					"threadId": "thread-1",
					"source":   "turn/outputDelta",
					"sequence": 100,
				}},
			})

			worker.Start()
			defer func() {
				ctx, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				_ = worker.Stop(ctx)
			}()

			waitForNotifySent(t, worker, 2)
			calls := broadcaster.observed()
			if len(calls) != 2 {
				t.Fatalf("NotifyAll call count = %d, want source plus standalone patch", len(calls))
			}
			assertNoEmbeddedThreadPatch(t, calls[0])
			if calls[1].method != eventsurface.MethodUIThreadPatch {
				t.Fatalf("second method = %q, want standalone patch", calls[1].method)
			}
		})
	}
}

func TestRPCPushWorkerSkipsEmbeddingWhenMatchingSourceIsAmbiguous(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodAgentMessageDelta, Payload: map[string]any{"threadId": "thread-1", "delta": "first"}},
		{Method: eventsurface.MethodAgentMessageDelta, Payload: map[string]any{"threadId": "thread-1", "delta": "second"}},
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{"threadId": "thread-1", "source": "turn/outputDelta", "sequence": 11}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 3)
	calls := broadcaster.observed()
	if len(calls) != 3 {
		t.Fatalf("NotifyAll call count = %d, want both sources and standalone patch", len(calls))
	}
	assertNoEmbeddedThreadPatch(t, calls[0])
	assertNoEmbeddedThreadPatch(t, calls[1])
	if calls[2].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("third method = %q, want standalone patch", calls[2].method)
	}
}

func TestRPCPushWorkerPreservesStandaloneWhenPatchArrivesBeforeSource(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{"threadId": "thread-1", "source": "turn/outputDelta", "sequence": 15}},
		{Method: eventsurface.MethodAgentMessageDelta, Payload: map[string]any{"threadId": "thread-1", "delta": "late source"}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want standalone patch plus source", len(calls))
	}
	if calls[0].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("first method = %q, want standalone patch", calls[0].method)
	}
	if calls[1].method != eventsurface.MethodAgentMessageDelta {
		t.Fatalf("second method = %q, want source", calls[1].method)
	}
	assertNoEmbeddedThreadPatch(t, calls[1])
}

func TestRPCPushWorkerPreservesStandaloneWhenSourceAlreadyDispatched(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodAgentMessageDelta, Payload: map[string]any{"threadId": "thread-1", "delta": "already sent"}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 1)
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{"threadId": "thread-1", "source": "turn/outputDelta", "sequence": 16}},
	})
	waitForNotifySent(t, worker, 2)

	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source then standalone patch", len(calls))
	}
	if calls[0].method != eventsurface.MethodAgentMessageDelta {
		t.Fatalf("first method = %q, want source", calls[0].method)
	}
	assertNoEmbeddedThreadPatch(t, calls[0])
	if calls[1].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("second method = %q, want standalone patch", calls[1].method)
	}
}

func TestRPCPushWorkerDoesNotBackfillWhenLatestMatchingSourceAlreadyHasPatch(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodAgentMessageDelta, Payload: map[string]any{"threadId": "thread-1", "delta": "first"}},
		{Method: eventsurface.MethodAgentMessageDelta, Payload: map[string]any{
			"threadId":     "thread-1",
			"delta":        "second",
			"_threadPatch": map[string]any{"sequence": 1},
		}},
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{"threadId": "thread-1", "source": "turn/outputDelta", "sequence": 12}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 3)
	calls := broadcaster.observed()
	if len(calls) != 3 {
		t.Fatalf("NotifyAll call count = %d, want both sources and standalone patch", len(calls))
	}
	assertNoEmbeddedThreadPatch(t, calls[0])
	embedded := embeddedThreadPatch(t, callPayloadMap(t, calls[1]))
	if embedded["sequence"] != 1 {
		t.Fatalf("latest source embedded sequence = %#v, want original 1", embedded["sequence"])
	}
	if calls[2].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("third method = %q, want standalone patch", calls[2].method)
	}
}

func TestRPCPushWorkerEmbedsStructPatchUsingSharedIdentityAliases(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodUIStateChanged, Payload: agentdto.StateChanged{
			AgentSessionHeader: shareddto.AgentSessionHeader{
				AgentHeader: shareddto.AgentHeader{
					ThreadHeader: shareddto.ThreadHeader{ThreadID: "thread-public-id"},
					AgentID:      "agent-1",
				},
			},
			NewState: "running",
		}},
		{Method: eventsurface.MethodUIThreadPatch, Payload: uidto.UIThreadPatch{
			ThreadID: "thread-public-id",
			Source:   "agent/stateChanged",
			Sequence: 13,
			Status:   "running",
		}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source plus standalone patch", len(calls))
	}
	embedded := embeddedThreadPatch(t, callPayloadMap(t, calls[0]))
	if embedded["threadId"] != "thread-public-id" {
		t.Fatalf("embedded threadId = %#v, want public thread id", embedded["threadId"])
	}
}

func TestRPCPushWorkerDoesNotCrossMatchThreadAndAgentIdentity(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodUIStateChanged, Payload: map[string]any{
			"threadId": "thread-A",
			"agentId":  "thread-B",
			"status":   "running",
		}},
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{
			"threadId": "thread-B",
			"source":   "agent/stateChanged",
			"sequence": 17,
		}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source plus standalone patch", len(calls))
	}
	assertNoEmbeddedThreadPatch(t, calls[0])
	if calls[1].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("second method = %q, want standalone patch", calls[1].method)
	}
}

func TestRPCPushWorkerDoesNotCrossMatchSparseAgentIdentity(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: eventsurface.MethodUIStateChanged, Payload: map[string]any{
			"agentId": "thread-B",
			"status":  "running",
		}},
		{Method: eventsurface.MethodUIThreadPatch, Payload: map[string]any{
			"threadId": "thread-B",
			"source":   "agent/stateChanged",
			"sequence": 18,
		}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source plus standalone patch", len(calls))
	}
	assertNoEmbeddedThreadPatch(t, calls[0])
	if calls[1].method != eventsurface.MethodUIThreadPatch {
		t.Fatalf("second method = %q, want standalone patch", calls[1].method)
	}
}

func TestRPCPushWorkerEmbedsToolDiffPatchIntoTurnDiffUpdated(t *testing.T) {
	broadcaster := &fakePushBroadcaster{}
	bridge := &PushBridge{logger: pkglogger.Get()}
	worker := newPushNotificationWorker(broadcaster, bridge, pkglogger.Get())
	worker.Enqueue([]eventsurface.Notification{
		{Method: "turn/diff/updated", Payload: map[string]any{"threadId": "thread-1", "diffText": "diff --git a/a b/a"}},
		{Method: eventsurface.MethodUIThreadPatch, Payload: uidto.UIThreadPatch{
			ThreadID:     "thread-1",
			Source:       "tool/diffUpdated",
			Sequence:     14,
			DiffText:     "diff --git a/a b/a",
			DiffRevision: 2,
		}},
	})

	worker.Start()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = worker.Stop(ctx)
	}()

	waitForNotifySent(t, worker, 2)
	calls := broadcaster.observed()
	if len(calls) != 2 {
		t.Fatalf("NotifyAll call count = %d, want source plus standalone patch", len(calls))
	}
	embedded := embeddedThreadPatch(t, callPayloadMap(t, calls[0]))
	if embedded["source"] != "tool/diffUpdated" {
		t.Fatalf("embedded source = %#v, want tool/diffUpdated", embedded["source"])
	}
}

func TestThreadPatchSourceMatchesMethodAliases(t *testing.T) {
	tests := []struct {
		name   string
		source string
		method string
		want   bool
	}{
		{name: "message delta raw", source: "turn/outputDelta", method: eventsurface.MethodAgentMessageDelta, want: true},
		{name: "message delta typed fallback", source: "turn/outputDelta", method: eventsurface.MethodTurnOutputDelta, want: true},
		{name: "reasoning is not message output", source: "turn/outputDelta", method: eventsurface.MethodReasoningTextDelta},
		{name: "stdout is not message output", source: "turn/outputDelta", method: eventsurface.MethodCommandOutputDelta},
		{name: "tool call", source: "tool/call", method: eventsurface.MethodToolCall, want: true},
		{name: "tool completed", source: "tool/completed", method: eventsurface.MethodItemCompleted, want: true},
		{name: "command approval requested", source: "tool/approvalRequested", method: eventsurface.MethodCommandApprovalRequested, want: true},
		{name: "file approval requested", source: "tool/approvalRequested", method: eventsurface.MethodFileApprovalRequested, want: true},
		{name: "skill approval requested", source: "tool/approvalRequested", method: eventsurface.MethodSkillApprovalRequested, want: true},
		{name: "approval resolved", source: "tool/approvalResolved", method: eventsurface.MethodApprovalResolved, want: true},
		{name: "diff updated raw source", source: "tool/diffUpdated", method: "turn/diff/updated", want: true},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if got := threadPatchSourceMatchesMethod(tc.source, tc.method); got != tc.want {
				t.Fatalf("threadPatchSourceMatchesMethod(%q, %q) = %v, want %v", tc.source, tc.method, got, tc.want)
			}
		})
	}
}
