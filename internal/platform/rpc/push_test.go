package rpc

import (
	"encoding/json"
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
			name: "retry progress error is suppressed",
			raw: providerdto.RawProviderEvent{
				EventType: "error",
				Data: map[string]any{
					"threadId":  "thread-1",
					"willRetry": true,
					"error": map[string]any{
						"message": "Reconnecting... 2/5",
					},
				},
			},
			want: []string{},
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

func TestProviderPushNotificationsForwardsGenericItemLifecycle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  providerdto.RawProviderEvent
		want []string
	}{
		{
			name: "generic item completed is forwarded",
			raw: providerdto.RawProviderEvent{
				EventType: eventsurface.MethodItemCompleted,
				Data:      map[string]any{"threadId": "thread-1", "itemId": "item-1"},
			},
			want: []string{
				eventsurface.MethodItemCompleted,
				eventsurface.MethodUIThreadChanged,
				eventsurface.MethodUISidebarChanged,
			},
		},
		{
			name: "generic item started is forwarded",
			raw: providerdto.RawProviderEvent{
				EventType: "item/started",
				Data:      map[string]any{"threadId": "thread-1", "itemId": "item-1"},
			},
			want: []string{
				"item/started",
				eventsurface.MethodUIThreadChanged,
				eventsurface.MethodUISidebarChanged,
			},
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

func TestProviderPushNotificationsForwardsToolShapedItemsWithoutCallID(t *testing.T) {
	t.Parallel()

	tests := []providerdto.RawProviderEvent{
		{EventType: "item/started", Data: map[string]any{
			"threadId": "thread-1",
			"item": map[string]any{
				"type": "function_call",
				"id":   "item-1",
				"name": "shell",
			},
		}},
		{EventType: eventsurface.MethodItemCompleted, Data: map[string]any{
			"threadId": "thread-1",
			"item": map[string]any{
				"type":      "function_call_output",
				"itemId":    "item-1",
				"tool_name": "shell",
			},
		}},
	}

	for _, raw := range tests {
		got := notificationMethods(providerPushNotifications(raw))
		if len(got) != 3 || got[0] != raw.EventType {
			t.Fatalf("providerPushNotifications(%q) = %#v, want raw event plus refreshes", raw.EventType, got)
		}
	}
}

func TestProviderPushNotificationsSkipsTypedAndMCPSurface(t *testing.T) {
	t.Parallel()

	tests := []providerdto.RawProviderEvent{
		{EventType: "turn/completed", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: "thread/started", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: eventsurface.MethodAgentMessageDelta, Data: map[string]any{"threadId": "thread-1"}},
		{EventType: eventsurface.MethodReasoningTextDelta, Data: map[string]any{"threadId": "thread-1"}},
		{EventType: "item/reasoning/summaryTextDelta", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: eventsurface.MethodCommandOutputDelta, Data: map[string]any{"threadId": "thread-1"}},
		{EventType: eventsurface.MethodToolCall, Data: map[string]any{"threadId": "thread-1"}},
		{EventType: eventsurface.MethodItemCompleted, Data: map[string]any{"threadId": "thread-1", "callId": "call-1", "name": "shell"}},
		{EventType: "item/started", Data: map[string]any{
			"threadId": "thread-1",
			"item": map[string]any{
				"type":   "function_call",
				"callId": "call-1",
				"name":   "shell",
			},
		}},
		{EventType: "agent/event/item_completed", Data: map[string]any{
			"threadId": "thread-1",
			"item": map[string]any{
				"type":     "mcp_tool_call_end",
				"callId":   "call-1",
				"toolName": "shell",
			},
		}},
		{EventType: eventsurface.MethodItemCompleted, Data: json.RawMessage(`{
			"threadId":"thread-1",
			"item":{
				"type":"function_call_output",
				"callId":"call-1",
				"tool_name":"shell"
			}
		}`)},
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
