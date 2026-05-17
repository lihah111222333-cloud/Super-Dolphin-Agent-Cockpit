package codexapp

import (
	"bytes"
	"strings"
	"testing"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
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
		Data:      map[string]any{"foo": "bar"},
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
