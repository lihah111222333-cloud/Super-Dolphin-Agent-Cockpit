package tools

import (
	"errors"
	"fmt"
	"testing"

	lspmanager "github.com/anthropic-ai/super-agent-v3/cmd/mcp-lsp/manager"
)

func TestToolErrorEnvelopeLanguageAgnostic(t *testing.T) {
	var baseline ToolErrorEnvelope
	for idx, languageID := range []string{"go", "python", "typescript"} {
		envelope := newToolErrorEnvelope("inspect", languageID, fmt.Errorf("%s route: %w", languageID, lspmanager.ErrUnsupportedLanguage))
		if envelope.Success {
			t.Fatalf("%s envelope success = true, want false", languageID)
		}
		if envelope.Code != "language_unsupported" {
			t.Fatalf("%s envelope code = %q, want language_unsupported", languageID, envelope.Code)
		}
		if envelope.Hint == "" {
			t.Fatalf("%s envelope hint is empty", languageID)
		}
		if envelope.Meta["language_id"] != languageID {
			t.Fatalf("%s envelope meta language_id = %#v", languageID, envelope.Meta["language_id"])
		}
		if idx == 0 {
			baseline = envelope
			continue
		}
		if envelope.Code != baseline.Code || envelope.Hint != baseline.Hint || envelope.Retryable != baseline.Retryable {
			t.Fatalf("%s envelope differs from Go baseline: got %#v want code=%q hint=%q retryable=%v", languageID, envelope, baseline.Code, baseline.Hint, baseline.Retryable)
		}
	}

	schemaEnvelope := newToolErrorEnvelope("file", "python", errors.New("decode params: json: unknown field \"line\""))
	if schemaEnvelope.Code != "schema_invalid" {
		t.Fatalf("schema envelope code = %q, want schema_invalid", schemaEnvelope.Code)
	}
	if schemaEnvelope.Meta["language_id"] != "python" {
		t.Fatalf("schema envelope language_id = %#v, want python", schemaEnvelope.Meta["language_id"])
	}
}
