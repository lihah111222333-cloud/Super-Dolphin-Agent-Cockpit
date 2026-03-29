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

	header := agentSessionHeader(payload)

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
