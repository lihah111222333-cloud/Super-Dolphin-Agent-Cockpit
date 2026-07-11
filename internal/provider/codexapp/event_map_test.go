package codexapp

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	dto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

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

func TestTranslateCodexEventWarnsOnUnknownRawEvent(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "mystery/event",
		Data:      map[string]any{"foo": "bar", "api_key": "sk-live-secret"},
	}, func(any) {
		t.Fatal("unknown raw event should not publish typed event")
	})

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
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "turn/completed",
		Data:      json.RawMessage(`{"agentId":"agent-1",`),
	}, func(ev any) {
		t.Fatalf("bad JSON published %#v, want no typed event", ev)
	})

	output := buf.String()
	if !strings.Contains(output, "invalid raw event payload") || !strings.Contains(output, "turn/completed") {
		t.Fatalf("warn output = %q, want invalid raw event payload warning", output)
	}
}

func TestTranslateCodexEventRejectsMissingCriticalIDs(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "turn/completed",
		Data: map[string]any{
			"agentId": "agent-1",
			"status":  "completed",
		},
	}, func(ev any) {
		t.Fatalf("missing turn_id published %#v, want no typed event", ev)
	})

	output := buf.String()
	if !strings.Contains(output, "invalid translated event") || !strings.Contains(output, "turn_id is required") {
		t.Fatalf("warn output = %q, want missing turn_id warning", output)
	}
}

func TestTranslateCodexEventSuppressesAccountRateLimitsUpdated(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "account/rateLimits/updated",
		Data:      map[string]any{"foo": "bar"},
	}, func(any) {
		t.Fatal("rate limit update should not publish typed event")
	})

	if output := buf.String(); strings.Contains(output, "unknown raw event") {
		t.Fatalf("output = %q, want no unknown raw event warning", output)
	}
}

func TestTranslateCodexEventSuppressesRetryProgressErrorWarning(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

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
	})

	if output := buf.String(); strings.Contains(output, "unknown raw event") {
		t.Fatalf("output = %q, want retry progress error warning suppressed", output)
	}
}

func TestTranslateCodexEventMCPStartupStatusOnlyWarnsOnFailures(t *testing.T) {
	var buf bytes.Buffer
	old := pkglogger.Get()
	pkglogger.InitWithConsoleWriter(&buf)
	t.Cleanup(func() { pkglogger.SetForTest(old) })

	translateCodexEvent(dto.RawProviderEvent{
		EventType: "mcpServer/startupStatus/updated",
		Data: map[string]any{
			"name":   "filesystem",
			"status": "ready",
		},
	}, func(any) {
		t.Fatal("mcp startup status should not publish typed event")
	})
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
	})
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
		})
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
	}, func(ev any) { got = append(got, ev) })

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
	}, func(ev any) { got = append(got, ev) })

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
	}, func(ev any) { got = append(got, ev) })

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	if end.Success || end.Error != "grep failed" {
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
	}, func(ev any) { got = append(got, ev) })

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
	}, func(ev any) { got = append(got, ev) })

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
	}, func(ev any) { got = append(got, ev) })

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
	}, func(ev any) { got = append(got, ev) })

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
	}, func(ev any) { got = append(got, ev) })

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
	}, func(ev any) { got = append(got, ev) })

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
	if end.Success || end.Error != "direct mcp failure" {
		t.Fatalf("ToolCallEnd = %+v, want direct MCP failure", end)
	}
	if !strings.Contains(end.Result, `"matches"`) || strings.Contains(end.Result, "plain text fallback") {
		t.Fatalf("Result = %q, want structuredContent before content text", end.Result)
	}
}

func TestToolCallEndReportsPersistFailure(t *testing.T) {
	providershared.SetCaptureToolResultHook(func(meta providershared.ToolResultMeta, raw string) providershared.ToolResultRecord {
		if meta.CallID != "call-grep" || meta.ToolName != "mcp__lsp__grep" {
			t.Fatalf("capture meta = %+v, want call-grep/mcp__lsp__grep", meta)
		}
		if !strings.Contains(raw, `"matches"`) {
			t.Fatalf("capture raw = %q, want structured preview", raw)
		}
		return providershared.ToolResultRecord{
			Preview:       `{"captured":true}`,
			PersistedPath: "/tmp/tool-result.json",
			PersistFailed: true,
			PersistError:  "disk full",
			Truncated:     true,
			OriginalSize:  1234,
		}
	})
	t.Cleanup(func() { providershared.SetCaptureToolResultHook(nil) })

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
	}, func(ev any) { got = append(got, ev) })

	if len(got) != 1 {
		t.Fatalf("published events = %d, want 1", len(got))
	}
	end, ok := got[0].(tooldto.ToolCallEnd)
	if !ok {
		t.Fatalf("event type = %T, want ToolCallEnd", got[0])
	}
	summary := struct {
		result, path, persistError string
		persistFailed, truncated   bool
		originalSize               int
	}{end.Result, end.PersistedPath, end.PersistError, end.PersistFailed, end.Truncated, end.OriginalSize}
	wantSummary := struct {
		result, path, persistError string
		persistFailed, truncated   bool
		originalSize               int
	}{`{"captured":true}`, "/tmp/tool-result.json", "disk full", true, true, 1234}
	if summary != wantSummary {
		t.Fatalf("ToolCallEnd capture fields = %+v, want %+v", summary, wantSummary)
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
	if end.Success || end.Error != "structured output error" {
		t.Fatalf("ToolCallEnd = %+v, want content text error", end)
	}
}
