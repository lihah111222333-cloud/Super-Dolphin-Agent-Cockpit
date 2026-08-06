package claudecli

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/observability"
)

func TestClaudeSessionEventUsesSafeErrorPreviewField(t *testing.T) {
	event := claudeSessionEvent("provider.session.start", startSpec{
		agentID:      "agent-1",
		publicThread: "thread-1",
	}, time.Millisecond, errors.New("claude failed token=sk-abcdefghijklmnopqrstuvwxyz"))

	preview, _ := event.Metadata[observability.ErrorPreviewField].(string)
	if !strings.Contains(preview, "claude failed") || strings.Contains(preview, "sk-") {
		t.Fatalf("error_preview = %q, want sanitized claude error", preview)
	}
	if event.Metadata[observability.ErrorCodeField] != "provider_error" {
		t.Fatalf("error_code = %#v, want provider_error", event.Metadata[observability.ErrorCodeField])
	}
	if event.Error != preview {
		t.Fatalf("event.Error = %q, want same safe preview %q", event.Error, preview)
	}
}

func TestClaudeTraceSpanCounterStaysMonotonicAcrossDriverAndSession(t *testing.T) {
	counter := newClaudeTraceSpanCounter()
	driver := &driver{traceSpanCounter: counter}
	session := &session{traceSpanCounter: driver.traceSpanCounter}
	driverEvent := observability.TraceEvent{Method: "provider.session.acquire"}
	sessionEvent := observability.TraceEvent{Method: "provider.turn.run"}

	fillClaudeTrace(context.Background(), &driverEvent, driver.traceSpanCounter)
	fillClaudeTrace(context.Background(), &sessionEvent, session.traceSpanCounter)
	if driverEvent.SpanID != "claude:provider.session.acquire:1" {
		t.Fatalf("driver span ID = %q", driverEvent.SpanID)
	}
	if sessionEvent.SpanID != "claude:provider.turn.run:2" {
		t.Fatalf("session span ID = %q", sessionEvent.SpanID)
	}
}
