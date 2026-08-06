package codexapp

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kelindar/event"

	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/claudecli"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestCodexEventTranslatorOwnsSamplerPerRegistration(t *testing.T) {
	first := newCodexEventTranslator(providershared.RuntimeHooks{})
	second := newCodexEventTranslator(providershared.RuntimeHooks{})
	if first.sampler == nil || second.sampler == nil || first.retryProgressPattern == nil || second.retryProgressPattern == nil {
		t.Fatal("newCodexEventTranslator() returned an incomplete runtime owner")
	}
	if first.sampler == second.sampler {
		t.Fatal("translators share sampler state, want one sampler per registration")
	}
	if first.retryProgressPattern == second.retryProgressPattern {
		t.Fatal("translators share retry-pattern state, want one owner per registration")
	}
}

func TestAgentSessionHeaderPrefersAgentIDAsThreadID(t *testing.T) {
	t.Parallel()

	payload := map[string]any{
		"threadId":  "019d3595-1444-76d0-adca-e7d9f6b11232",
		"agentId":   "agent_1774720455588_04820e21fd876e3b",
		"sessionId": "019d3595-1444-76d0-adca-e7d9f6b11232",
	}

	header := buildAgentSessionHeader(payload)

	// ThreadID must be the agentId, not the codex UUID.
	if got := header.ThreadID; got != "agent_1774720455588_04820e21fd876e3b" {
		t.Fatalf("ThreadID = %q, want agentId", got)
	}
	// AgentID unchanged.
	if got := header.AgentID; got != "agent_1774720455588_04820e21fd876e3b" {
		t.Fatalf("AgentID = %q, want agentId", got)
	}
	// SessionID should still be the codex UUID.
	if got := header.SessionID; got != "019d3595-1444-76d0-adca-e7d9f6b11232" {
		t.Fatalf("SessionID = %q, want codex UUID", got)
	}
}

func TestThreadStartedPreservesProviderThreadIDSeparatelyFromAgentID(t *testing.T) {
	t.Parallel()

	const agentID = "agent_1785230940369872000"
	const providerThreadID = "019fa80e-3ddc-7d51-87a2-90a76e2f5c74"
	event, ok := translateAgentEvent("thread/started", map[string]any{
		"agentId":  agentID,
		"threadId": providerThreadID,
	})
	if !ok {
		t.Fatal("translateAgentEvent() ok = false, want true")
	}
	launched, ok := event.(agentdto.AgentLaunched)
	if !ok {
		t.Fatalf("event type = %T, want agentdto.AgentLaunched", event)
	}
	if launched.AgentID != agentID || launched.ThreadID != agentID {
		t.Fatalf("public identity = (%q, %q), want internal agent id %q", launched.AgentID, launched.ThreadID, agentID)
	}
	if launched.ProviderThreadID != providerThreadID {
		t.Fatalf("ProviderThreadID = %q, want %q", launched.ProviderThreadID, providerThreadID)
	}
}

func TestCodexTerminalTimestampUsesProviderValue(t *testing.T) {
	want, err := time.Parse(time.RFC3339Nano, "2026-07-16T10:11:12.123Z")
	if err != nil {
		t.Fatalf("parse fixture timestamp: %v", err)
	}
	var published []any
	translateCodexAdapterEvent(dto.RawProviderEvent{EventType: "turn/completed", Data: map[string]any{
		"agentId": "agent-1", "threadId": "thread-1", "turnId": "turn-1", "timestamp": want.Format(time.RFC3339Nano), "success": true,
	}}, func(ev any) { published = append(published, ev) })
	if len(published) != 1 {
		t.Fatalf("published events = %#v, want one terminal event", published)
	}
	completed, ok := published[0].(turndto.TurnCompleted)
	if !ok {
		t.Fatalf("event type = %T, want TurnCompleted", published[0])
	}
	if !completed.Timestamp.Equal(want) {
		t.Fatalf("terminal timestamp = %v, want provider timestamp %v", completed.Timestamp, want)
	}
}

func TestCodexTimestampValidationDoesNotRequireToolBeginTimestamp(t *testing.T) {
	var published []any
	translateCodexAdapterEvent(dto.RawProviderEvent{EventType: "item/tool/call", Data: map[string]any{
		"agentId": "agent-1", "threadId": "thread-1", "turnId": "turn-1", "callId": "call-1", "toolName": "Read",
	}}, func(ev any) { published = append(published, ev) })
	if len(published) != 1 {
		t.Fatalf("published events = %#v, want one tool begin event", published)
	}
	if _, ok := published[0].(tooldto.ToolCallBegin); !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", published[0])
	}
}

func TestCodexTimestampValidationMatchesClaudeProviderErrors(t *testing.T) {
	tests := []struct {
		name     string
		register func(*unified.EventDispatcher)
		raw      dto.RawProviderEvent
		wantCode string
	}{
		{
			name:     "codex missing lifecycle timestamp",
			register: func(dispatcher *unified.EventDispatcher) { RegisterTranslators(dispatcher, testRuntimeHooks(t)) },
			raw: dto.RawProviderEvent{EventType: "thread/status/changed", Data: map[string]any{
				"agentId": "agent-1", "threadId": "thread-1", "newState": "idle",
			}},
			wantCode: codexMissingTimestampCode,
		},
		{
			name: "claude missing lifecycle timestamp",
			register: func(dispatcher *unified.EventDispatcher) {
				claudecli.RegisterTranslators(dispatcher, testRuntimeHooks(t))
			},
			raw: dto.RawProviderEvent{EventType: "system:init", Data: map[string]any{
				"agent_id": "agent-1", "thread_id": "thread-1", "session_id": "session-1",
			}},
			wantCode: "claude_missing_timestamp",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := event.NewDispatcher()
			defer func() { _ = bus.Close() }()
			dispatcher := unified.NewEventDispatcher(bus, nil)
			tt.register(dispatcher)
			agentErrors := make(chan agentdto.AgentError, 1)
			cancel := event.Subscribe(bus, func(ev agentdto.AgentError) { agentErrors <- ev })
			defer cancel()
			dispatcher.Dispatch(tt.raw)
			select {
			case got := <-agentErrors:
				if got.Code != tt.wantCode ||
					!strings.HasPrefix(got.Message, "Provider reported an error. Diagnostic ID: ") ||
					strings.Contains(got.Message, "missing timestamp") {
					t.Fatalf("AgentError = %#v, want safe diagnostic with code %q", got, tt.wantCode)
				}
			case <-time.After(time.Second):
				t.Fatalf("timed out waiting for provider timestamp error")
			}
		})
	}
}

func TestTranslateCodexEventWarnsOnUnknownRawEvent(t *testing.T) {
	var buf bytes.Buffer
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(&buf)
	runtime.BindDefault()

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "mystery/event",
		Data:      map[string]any{"foo": "bar", "api_key": "sk-live-secret"},
	}, func(any) {
		t.Fatal("unknown raw event should not publish typed event")
	}, testRuntimeHooks(t))

	output := buf.String()
	if !strings.Contains(output, "unknown raw event") {
		t.Fatalf("warn output = %q, want unknown raw event warning", output)
	}
	if !strings.Contains(output, "mystery/event") {
		t.Fatalf("warn output = %q, want raw event type", output)
	}
	for _, forbidden := range []string{"sk-live-secret", "api_key"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("warn output leaked %q: %q", forbidden, output)
		}
	}
	for _, want := range []string{"payload_sha256", "payload_size_bytes"} {
		if !strings.Contains(output, want) {
			t.Fatalf("warn output = %q, want %q metadata", output, want)
		}
	}
}

func TestTranslateCodexEventRejectsBadJSONPayload(t *testing.T) {
	var buf bytes.Buffer
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(&buf)
	runtime.BindDefault()

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "turn/completed",
		Data:      json.RawMessage(`{"agentId":"agent-1",`),
	}, func(ev any) {
		t.Fatalf("bad JSON published %#v, want no typed event", ev)
	}, testRuntimeHooks(t))

	output := buf.String()
	if !strings.Contains(output, "invalid raw event payload") || !strings.Contains(output, "turn/completed") {
		t.Fatalf("warn output = %q, want invalid raw event payload warning", output)
	}
}

func TestTranslateCodexEventRejectsMissingCriticalIDs(t *testing.T) {
	var buf bytes.Buffer
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(&buf)
	runtime.BindDefault()

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "turn/completed",
		Data: map[string]any{
			"agentId":   "agent-1",
			"status":    "completed",
			"timestamp": "2026-07-16T10:11:12.123Z",
		},
	}, func(ev any) {
		t.Fatalf("missing turn_id published %#v, want no typed event", ev)
	}, testRuntimeHooks(t))

	output := buf.String()
	if !strings.Contains(output, "invalid translated event") || !strings.Contains(output, "turn_id is required") {
		t.Fatalf("warn output = %q, want missing turn_id warning", output)
	}
}

func TestTranslateCodexEventSuppressesAccountRateLimitsUpdated(t *testing.T) {
	var buf bytes.Buffer
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(&buf)
	runtime.BindDefault()

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "account/rateLimits/updated",
		Data:      map[string]any{"foo": "bar"},
	}, func(any) {
		t.Fatal("rate limit update should not publish typed event")
	}, testRuntimeHooks(t))

	if output := buf.String(); strings.Contains(output, "unknown raw event") {
		t.Fatalf("output = %q, want no unknown raw event warning", output)
	}
}

func TestTranslateCodexEventSuppressesRetryProgressErrorWarning(t *testing.T) {
	var buf bytes.Buffer
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(&buf)
	runtime.BindDefault()

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "error",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "thread-1",
			"turnId":    "turn-1",
			"willRetry": true,
			"error": map[string]any{
				"message":           "Reconnecting... 2/5",
				"additionalDetails": "request timed out",
			},
		},
	}, func(ev any) {
		t.Fatalf("retry progress error published %#v, want no typed event", ev)
	}, testRuntimeHooks(t))

	if output := buf.String(); strings.Contains(output, "unknown raw event") {
		t.Fatalf("output = %q, want retry progress error warning suppressed", output)
	}
}

func TestTranslateCodexEventMCPStartupStatusOnlyWarnsOnFailures(t *testing.T) {
	var buf bytes.Buffer
	runtime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	runtime.InitWithConsoleWriter(&buf)
	runtime.BindDefault()

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "mcpServer/startupStatus/updated",
		Data: map[string]any{
			"name":   "filesystem",
			"status": "ready",
		},
	}, func(any) {
		t.Fatal("mcp startup status should not publish typed event")
	}, testRuntimeHooks(t))
	if output := buf.String(); strings.Contains(output, "mcp server startup status") {
		t.Fatalf("ready output = %q, want debug-only/no info warning", output)
	}

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "mcpServer/startupStatus/updated",
		Data: map[string]any{
			"name":   "filesystem",
			"status": "failed",
			"error":  "boom",
		},
	}, func(any) {
		t.Fatal("mcp startup status should not publish typed event")
	}, testRuntimeHooks(t))
	output := buf.String()
	if !strings.Contains(output, "mcp server startup status") || !strings.Contains(output, "failed") {
		t.Fatalf("failed output = %q, want warning", output)
	}
}

// TestTranslateCodexEventIgnoresClaudeColonTurnEvents locks that the codex
// translator does not claim claude's colon-style turn events. The unified
// EventDispatcher broadcasts every raw event to all translators; before
// this fix codex also translated claude's turn:complete into a second
// TurnCompleted (with the report text mis-mapped into the Error field).
// Colon-style turn events belong to the claude translator only.
func TestTranslateCodexEventIgnoresClaudeColonTurnEvents(t *testing.T) {
	for _, method := range []string{"turn:complete", "turn:interrupted", "turn:started"} {
		translateCodexEvent(dto.RawProviderEvent{
			EventType: method,
			Data:      map[string]any{"turnId": "T1", "success": true, "message": "done"},
		}, func(ev any) {
			t.Fatalf("translateCodexEvent(%q) published %#v, want no typed event", method, ev)
		}, testRuntimeHooks(t))
	}
}

func TestTranslateToolApprovalResolvedPreservesRequestID(t *testing.T) {
	event, ok := translateToolEvent("approval/resolved", map[string]any{
		"requestId": int64(73),
		"threadId":  "thread-1",
		"callId":    "call-1",
		"approved":  true,
	})
	if !ok {
		t.Fatal("translateToolEvent() ok = false, want true")
	}
	resolved, ok := event.(tooldto.ToolApprovalResolved)
	if !ok {
		t.Fatalf("event type = %T, want ToolApprovalResolved", event)
	}
	encoded, err := json.Marshal(resolved)
	if err != nil {
		t.Fatalf("marshal resolved approval: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("unmarshal resolved approval: %v", err)
	}
	if payload["request_id"] != float64(73) {
		t.Fatalf("request_id = %#v, want 73", payload["request_id"])
	}
}

func TestTranslateToolEventUsesNameFieldForDynamicToolBegin(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "item/tool/call",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "provider-thread-1",
			"turnId":    "turn-1",
			"callId":    "call-1",
			"name":      "file",
			"arguments": map[string]any{"action": "read_file", "file_path": "smoke.go"},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	begin, ok := got[0].(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", got[0])
	}
	if begin.ToolName != "file" {
		t.Fatalf("ToolName = %q, want file", begin.ToolName)
	}
}

func TestTranslateToolEventUsesNameFieldForDynamicToolEnd(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "item/completed",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "provider-thread-1",
			"turnId":   "turn-1",
			"callId":   "call-1",
			"name":     "patch_edit",
			"success":  true,
			"result":   map[string]any{"success": true, "text_edit_count": 2},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.ToolName != "patch_edit" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful patch_edit end", end)
	}
}

func TestTranslateToolEventMarksNestedResultFailure(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "item/completed",
		Data: map[string]any{
			"agentId": "agent-1",
			"turnId":  "turn-1",
			"callId":  "call-1",
			"name":    "grep",
			"result": map[string]any{
				"success": false,
				"error":   "grep failed",
			},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.Success || !strings.HasPrefix(end.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.Error, "grep failed") {
		t.Fatalf("ToolCallEnd = %+v, want nested result failure", end)
	}
}

func TestTranslateCodexRolloutFunctionCallPublishesToolCallBegin(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "response_item",
		Data: map[string]any{
			"agentId":   "agent-1",
			"threadId":  "provider-thread-1",
			"turnId":    "turn-1",
			"type":      "function_call",
			"name":      "file",
			"namespace": "mcp__lsp__",
			"arguments": `{"action":"read_file","file_path":"smoke.go"}`,
			"call_id":   "call-file",
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	begin, ok := got[0].(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", got[0])
	}
	if begin.CallID != "call-file" || begin.ToolName != "mcp__lsp__file" {
		t.Fatalf("ToolCallBegin = %+v, want call-file/mcp__lsp__file", begin)
	}
	if begin.ArgumentsPreview != `{"action":"read_file","file_path":"[REDACTED]"}` {
		t.Fatalf("ArgumentsPreview = %q, want sanitized JSON arguments", begin.ArgumentsPreview)
	}
}

func TestTranslateCodexRolloutToolCallPublishesToolCallBegin(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "response_item",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "provider-thread-1",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type":    "tool_call",
				"name":    "grep",
				"call_id": "call-grep",
				"arguments": map[string]any{
					"pattern": "ToolCallBegin",
					"path":    "internal/provider/codexapp",
				},
			},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	begin, ok := got[0].(tooldto.ToolCallBegin)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallBegin", got[0])
	}
	if begin.CallID != "call-grep" || begin.ToolName != "grep" {
		t.Fatalf("ToolCallBegin = %+v, want call-grep/grep", begin)
	}
	if !strings.Contains(begin.ArgumentsPreview, "ToolCallBegin") {
		t.Fatalf("ArgumentsPreview = %q, want grep arguments", begin.ArgumentsPreview)
	}
}

func TestTranslateCodexRolloutMCPToolCallEndPublishesToolCallEnd(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "event_msg",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "provider-thread-1",
			"turnId":   "turn-1",
			"type":     "mcp_tool_call_end",
			"call_id":  "call-patch-edit",
			"invocation": map[string]any{
				"server":    "lsp",
				"tool":      "patch_edit",
				"arguments": map[string]any{"action": "replace_range", "file_path": "smoke.go"},
			},
			"duration": map[string]any{"secs": float64(0), "nanos": float64(4349125)},
			"result": map[string]any{
				"Ok": map[string]any{
					"content": []any{map[string]any{
						"type": "text",
						"text": `{"success":true,"text_edit_count":1}`,
					}},
					"structuredContent": map[string]any{"success": true, "text_edit_count": float64(1)},
					"isError":           false,
				},
			},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.CallID != "call-patch-edit" || end.ToolName != "mcp__lsp__patch_edit" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful patch_edit end", end)
	}
	if !strings.Contains(end.Result, `"text_edit_count":1`) {
		t.Fatalf("Result = %q, want tool result preview", end.Result)
	}
	if end.ElapsedMS != 4 {
		t.Fatalf("ElapsedMS = %d, want 4", end.ElapsedMS)
	}
}

func TestTranslateCodexRolloutToolResultPrefersStructuredContentPreview(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "event_msg",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "provider-thread-1",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type":    "tool_result",
				"call_id": "call-grep",
				"invocation": map[string]any{
					"server": "lsp",
					"tool":   "grep",
				},
				"duration": map[string]any{"secs": float64(0), "nanos": float64(3123456)},
				"result": map[string]any{
					"Ok": map[string]any{
						"content": []any{map[string]any{
							"type": "text",
							"text": "plain text fallback",
						}},
						"structuredContent": map[string]any{
							"success": true,
							"matches": []any{"internal/provider/codexapp/event_map.go"},
						},
						"isError": false,
					},
				},
			},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.CallID != "call-grep" || end.ToolName != "mcp__lsp__grep" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful call-grep/mcp__lsp__grep", end)
	}
	if !strings.Contains(end.Result, `"matches"`) {
		t.Fatalf("Result = %q, want structuredContent preview", end.Result)
	}
	if strings.Contains(end.Result, "plain text fallback") {
		t.Fatalf("Result = %q, want structuredContent before content text", end.Result)
	}
	if end.ElapsedMS != 3 {
		t.Fatalf("ElapsedMS = %d, want 3", end.ElapsedMS)
	}
}

func TestTranslateCodexRolloutToolResultSupportsLowercaseOKWrapper(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "event_msg",
		Data: map[string]any{
			"agentId": "agent-1",
			"turnId":  "turn-1",
			"item": map[string]any{
				"type":    "tool_result",
				"call_id": "call-grep",
				"invocation": map[string]any{
					"server": "lsp",
					"tool":   "grep",
				},
				"result": map[string]any{
					"ok": map[string]any{
						"content": []any{map[string]any{
							"type": "text",
							"text": "plain text fallback",
						}},
						"structuredContent": map[string]any{
							"success": true,
							"total":   float64(2),
						},
						"isError": false,
					},
				},
			},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.CallID != "call-grep" || end.ToolName != "mcp__lsp__grep" || !end.Success {
		t.Fatalf("ToolCallEnd = %+v, want successful lowercase ok result", end)
	}
	if !strings.Contains(end.Result, `"total":2`) || strings.Contains(end.Result, "plain text fallback") {
		t.Fatalf("Result = %q, want lowercase ok structuredContent before content text", end.Result)
	}
}

func TestTranslateCodexRolloutResponseItemToolResultSupportsDirectMCPResult(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "response_item",
		Data: map[string]any{
			"agentId":  "agent-1",
			"threadId": "provider-thread-1",
			"turnId":   "turn-1",
			"item": map[string]any{
				"type":     "tool_result",
				"call_id":  "call-grep",
				"toolName": "grep",
				"result": map[string]any{
					"content": []any{map[string]any{
						"type": "text",
						"text": "plain text fallback",
					}},
					"structuredContent": map[string]any{
						"success": false,
						"error":   "direct mcp failure",
						"matches": []any{"internal/provider/codexapp/event_map.go"},
					},
					"isError": true,
				},
			},
		},
	}, func(ev any) { got = append(got, ev) }, testRuntimeHooks(t))

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.CallID != "call-grep" || end.ToolName != "grep" {
		t.Fatalf("ToolCallEnd = %+v, want call-grep/grep", end)
	}
	if end.Success || !strings.HasPrefix(end.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.Error, "direct mcp failure") {
		t.Fatalf("ToolCallEnd = %+v, want public MCP failure", end)
	}
	if !strings.Contains(end.Result, `"matches"`) || strings.Contains(end.Result, "plain text fallback") {
		t.Fatalf("Result = %q, want structuredContent before content text", end.Result)
	}
}

func TestToolCallEndReportsPersistFailure(t *testing.T) {
	hooks := configureCaptureRuntimeHookForTest(t, func(meta providershared.ToolResultMeta, raw string) (providershared.ToolResultRecord, error) {
		return persistFailureCaptureResult(t, meta, raw)
	})

	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "event_msg",
		Data: map[string]any{
			"agentId": "agent-1",
			"turnId":  "turn-1",
			"item": map[string]any{
				"type":    "tool_result",
				"call_id": "call-grep",
				"invocation": map[string]any{
					"server": "lsp",
					"tool":   "grep",
				},
				"result": map[string]any{
					"Ok": map[string]any{
						"structuredContent": map[string]any{"matches": []any{"a.go"}},
						"isError":           false,
					},
				},
			},
		},
	}, func(ev any) { got = append(got, ev) }, hooks)

	assertPersistFailureToolEnd(t, got)
}

func persistFailureCaptureResult(t *testing.T, meta providershared.ToolResultMeta, raw string) (providershared.ToolResultRecord, error) {
	t.Helper()
	if meta.CallID != "call-grep" || meta.ToolName != "mcp__lsp__grep" {
		t.Fatalf("capture meta = %+v, want call-grep/mcp__lsp__grep", meta)
	}
	if !strings.Contains(raw, `"matches"`) {
		t.Fatalf("capture raw = %q, want structured preview", raw)
	}
	return providershared.ToolResultRecord{
		Preview: `{"captured":true}`, PersistedPath: "/tmp/tool-result.json", PersistFailed: true,
		PersistError: "disk full", Truncated: true, OriginalSize: 1234,
	}, nil
}

func assertPersistFailureToolEnd(t *testing.T, got []any) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.Result != `{"captured":true}` || end.PersistedPath != "/tmp/tool-result.json" || !end.PersistFailed || !end.Truncated || end.OriginalSize != 1234 {
		t.Fatalf("ToolCallEnd capture fields = %+v", end)
	}
	if !strings.HasPrefix(end.PersistError, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.PersistError, "disk full") {
		t.Fatalf("ToolCallEnd persist error = %q, want public diagnostic", end.PersistError)
	}
}

// TestToolCallEndFailsWhenRuntimeCaptureFails 验证捕获依赖错误不会退化成成功事件。
func TestToolCallEndFailsWhenRuntimeCaptureFails(t *testing.T) {
	hooks := configureCaptureRuntimeHookForTest(t, func(providershared.ToolResultMeta, string) (providershared.ToolResultRecord, error) {
		return providershared.ToolResultRecord{}, errors.New("capture unavailable")
	})

	ev, ok := translateToolEvent("tool.call.end", map[string]any{
		"threadId": "thread-1",
		"turnId":   "turn-1",
		"callId":   "call-1",
		"toolName": "Read",
		"success":  true,
		"result":   "raw",
	}, hooks)
	if !ok {
		t.Fatal("translateToolEvent() ok = false, want ToolCallEnd")
	}
	end, ok := ev.(tooldto.ToolCallEnd)
	if !ok || end.Success || !strings.HasPrefix(end.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.Error, "capture unavailable") {
		t.Fatalf("ToolCallEnd = %+v, want public capture failure", ev)
	}
}

func TestTranslateCodexRolloutFunctionCallOutputWithoutToolNameIsIgnored(t *testing.T) {
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "event_msg",
		Data: map[string]any{
			"agentId": "agent-1",
			"turnId":  "turn-1",
			"item": map[string]any{
				"type":    "function_call_output",
				"call_id": "call-file",
				"output":  `{"success":true}`,
			},
		},
	}, func(ev any) {
		t.Fatalf("function_call_output without ToolName published %#v, want no typed event", ev)
	})
}

func TestTranslateCodexRolloutMCPToolCallEndMarksToolError(t *testing.T) {
	var got []any
	translateCodexEvent(dto.RawProviderEvent{
		EventType: "event_msg",
		Data: map[string]any{
			"agentId": "agent-1",
			"turnId":  "turn-1",
			"type":    "mcp_tool_call_end",
			"call_id": "call-file",
			"invocation": map[string]any{
				"server": "lsp",
				"tool":   "file",
			},
			"result": map[string]any{
				"Ok": map[string]any{
					"content": []any{map[string]any{
						"type": "text",
						"text": "structured output error",
					}},
					"isError": true,
				},
			},
		},
	}, func(ev any) { got = append(got, ev) })

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.Success || !strings.HasPrefix(end.Error, "Tool execution failed. Diagnostic ID: ") || strings.Contains(end.Error, "structured output error") {
		t.Fatalf("ToolCallEnd = %+v, want content text error", end)
	}
}
