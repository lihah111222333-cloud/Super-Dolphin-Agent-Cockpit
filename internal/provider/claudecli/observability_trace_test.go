package claudecli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
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
