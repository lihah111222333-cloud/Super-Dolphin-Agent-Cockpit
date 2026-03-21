package rpc

import (
	"reflect"
	"testing"

	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/eventsurface"
)

func TestProviderPushNotificationsForwardsCriticalSurface(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  providerdto.RawProviderEvent
		want []string
	}{
		{
			name: "approval legacy method normalized and refreshed",
			raw: providerdto.RawProviderEvent{
				EventType: legacyApprovalEventMethod,
				Data:      map[string]any{"threadId": "thread-1", "reason": "approval needed"},
			},
			want: []string{
				approvalCallbackMethodCommandExecution,
				eventsurface.MethodUIThreadChanged,
				eventsurface.MethodUISidebarChanged,
			},
		},
		{
			name: "plan delta is forwarded",
			raw: providerdto.RawProviderEvent{
				EventType: "item/plan/delta",
				Data:      map[string]any{"threadId": "thread-1", "delta": "next"},
			},
			want: []string{
				"item/plan/delta",
				eventsurface.MethodUIThreadChanged,
				eventsurface.MethodUISidebarChanged,
			},
		},
		{
			name: "error is forwarded",
			raw: providerdto.RawProviderEvent{
				EventType: "error",
				Data:      map[string]any{"threadId": "thread-1", "message": "boom"},
			},
			want: []string{
				"error",
				eventsurface.MethodUIThreadChanged,
				eventsurface.MethodUISidebarChanged,
			},
		},
		{
			name: "token usage remains available",
			raw: providerdto.RawProviderEvent{
				EventType: "thread/tokenUsage/updated",
				Data:      map[string]any{"threadId": "thread-1", "totalTokens": 42},
			},
			want: []string{"thread/tokenUsage/updated"},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := notificationMethods(providerPushNotifications(tc.raw))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("providerPushNotifications(%q) = %#v, want %#v", tc.raw.EventType, got, tc.want)
			}
		})
	}
}

func TestProviderPushNotificationsSkipsTypedAndMCPSurface(t *testing.T) {
	t.Parallel()

	tests := []providerdto.RawProviderEvent{
		{EventType: "turn/completed", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: "thread/started", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: "mcpServer/oauthLogin/completed", Data: map[string]any{"threadId": "thread-1"}},
	}

	for _, raw := range tests {
		if got := providerPushNotifications(raw); len(got) != 0 {
			t.Fatalf("providerPushNotifications(%q) = %#v, want nil", raw.EventType, notificationMethods(got))
		}
	}
}

func notificationMethods(notifications []eventsurface.Notification) []string {
	out := make([]string, 0, len(notifications))
	for _, notification := range notifications {
		out = append(out, notification.Method)
	}
	return out
}
