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
			name: "approval legacy method is covered by typed surface",
			raw: providerdto.RawProviderEvent{
				EventType: legacyApprovalEventMethod,
				Data:      map[string]any{"threadId": "thread-1", "reason": "approval needed"},
			},
			want: []string{},
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
			name: "token usage is covered by typed surface",
			raw: providerdto.RawProviderEvent{
				EventType: "thread/tokenUsage/updated",
				Data:      map[string]any{"threadId": "thread-1", "totalTokens": 42},
			},
			want: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := notificationMethods(providerPushNotifications(tc.raw))
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("providerPushNotifications(%q) = %#v, want %#v", tc.raw.EventType, got, tc.want)
			}
		})
	}
}

func TestShouldPushRawProviderMethodUsesEventSurfaceAllowlist(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		method string
		want   bool
	}{
		{name: "legacy approval alias normalizes to typed method", method: legacyApprovalEventMethod, want: false},
		{name: "raw item prefix allowed", method: "item/plan/delta", want: true},
		{name: "raw suffix allowed", method: "item/custom/requestApproval", want: true},
		{name: "typed method suppressed", method: eventsurface.MethodThreadStarted, want: false},
		{name: "workspace run remains compat only", method: "workspace/run/created", want: false},
		{name: "bridge control rpc failed rejected", method: "rpc.failed", want: false},
		{name: "bridge control api rpc failed rejected", method: "api.rpc.failed", want: false},
		{name: "bridge control task node status rejected", method: "task/node/statuschanged", want: false},
		{name: "bridge control failed suffix rejected", method: "thread.send/failed", want: false},
		{name: "unknown rejected", method: "unknown/domain/event", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := shouldPushRawProviderMethod(tt.method); got != tt.want {
				t.Fatalf("shouldPushRawProviderMethod(%q) = %v, want %v", tt.method, got, tt.want)
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
			name: "generic item completed is covered by typed surface",
			raw: providerdto.RawProviderEvent{
				EventType: eventsurface.MethodItemCompleted,
				Data:      map[string]any{"threadId": "thread-1", "itemId": "item-1"},
			},
			want: []string{},
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
		if raw.EventType == eventsurface.MethodItemCompleted {
			if len(got) != 0 {
				t.Fatalf("providerPushNotifications(%q) = %#v, want typed method suppressed", raw.EventType, got)
			}
			continue
		}
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
		{EventType: "rpc.failed", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: "api.rpc.failed", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: "task/node/statuschanged", Data: map[string]any{"threadId": "thread-1"}},
		{EventType: "thread.send/failed", Data: map[string]any{"threadId": "thread-1"}},
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
