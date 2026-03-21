package eventsurface

import "testing"

func TestExpandNotificationsAddsLegacyThreadRefresh(t *testing.T) {
	t.Parallel()

	got := ExpandNotifications(MethodThreadStarted, map[string]any{
		"threadId": "thread-1",
		"agentId":  "agent-1",
	})
	if len(got) != 3 {
		t.Fatalf("len(ExpandNotifications()) = %d, want 3", len(got))
	}
	assertNotification(t, got[0], MethodThreadStarted, map[string]any{
		"threadId": "thread-1",
		"agentId":  "agent-1",
	})
	assertNotification(t, got[1], MethodUIThreadChanged, map[string]any{
		"source":   MethodThreadStarted,
		"threadId": "thread-1",
		"agent_id": "agent-1",
	})
	assertNotification(t, got[2], MethodUISidebarChanged, map[string]any{
		"source":   MethodThreadStarted,
		"threadId": "thread-1",
		"agent_id": "agent-1",
	})
}

func TestExpandNotificationsAddsWorkspaceSidebarRefresh(t *testing.T) {
	t.Parallel()

	got := ExpandNotifications(MethodWorkspaceCreated, map[string]any{"runKey": "run-1"})
	if len(got) != 2 {
		t.Fatalf("len(ExpandNotifications()) = %d, want 2", len(got))
	}
	assertNotification(t, got[0], MethodWorkspaceCreated, map[string]any{"runKey": "run-1"})
	assertNotification(t, got[1], MethodUISidebarChanged, map[string]any{
		"source": MethodWorkspaceCreated,
	})
}

func TestExpandNotificationsSkipsLegacyRefreshForDirectThreadPushes(t *testing.T) {
	t.Parallel()

	for _, method := range []string{
		MethodThreadTokenUsage,
		MethodThreadCompacted,
		MethodUIThreadPatch,
		MethodAgentMessageDelta,
		MethodReasoningTextDelta,
		MethodCommandOutputDelta,
		MethodTurnOutputDelta,
	} {
		got := ExpandNotifications(method, map[string]any{"threadId": "thread-1"})
		if len(got) != 1 {
			t.Fatalf("len(ExpandNotifications(%q)) = %d, want 1", method, len(got))
		}
		assertNotification(t, got[0], method, map[string]any{"threadId": "thread-1"})
	}
}

func assertNotification(t *testing.T, got Notification, wantMethod string, wantPayload map[string]any) {
	t.Helper()

	if got.Method != wantMethod {
		t.Fatalf("notification method = %q, want %q", got.Method, wantMethod)
	}
	payload, _ := got.Payload.(map[string]any)
	if len(payload) != len(wantPayload) {
		t.Fatalf("payload = %#v, want %#v", payload, wantPayload)
	}
	for key, want := range wantPayload {
		if payload[key] != want {
			t.Fatalf("payload[%q] = %#v, want %#v; payload=%#v", key, payload[key], want, payload)
		}
	}
}
