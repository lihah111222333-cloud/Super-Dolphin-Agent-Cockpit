package tools

import (
	"errors"
	"fmt"
	"testing"

	lspmanager "github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/manager"
)

func requireUnsupportedLanguageEnvelope(t *testing.T, envelope ToolErrorEnvelope, languageID string) {
	t.Helper()
	if envelope.Success {
		t.Fatalf("%s envelope success = true, want false", languageID)
	}
	if envelope.Code != "language_unsupported" {
		t.Fatalf("%s envelope code = %q, want language_unsupported", languageID, envelope.Code)
	}
	if envelope.Meta["language_id"] != languageID {
		t.Fatalf("%s envelope meta language_id = %#v", languageID, envelope.Meta["language_id"])
	}
}

func requireEnvelopeMatchesBaseline(t *testing.T, languageID string, envelope, baseline ToolErrorEnvelope) {
	t.Helper()
	if envelope.Code != baseline.Code {
		t.Fatalf("%s envelope code = %q, want %q", languageID, envelope.Code, baseline.Code)
	}
	if envelope.Hint != baseline.Hint {
		t.Fatalf("%s envelope hint = %q, want %q", languageID, envelope.Hint, baseline.Hint)
	}
	if envelope.Retryable != baseline.Retryable {
		t.Fatalf("%s envelope retryable = %v, want %v", languageID, envelope.Retryable, baseline.Retryable)
	}
}

func TestToolErrorEnvelopeLanguageAgnostic(t *testing.T) {
	var baseline ToolErrorEnvelope
	for idx, languageID := range []string{"go", "python", "typescript"} {
		envelope := newToolErrorEnvelope("inspect", languageID, fmt.Errorf("%s route: %w", languageID, lspmanager.ErrUnsupportedLanguage))
		requireUnsupportedLanguageEnvelope(t, envelope, languageID)
		if envelope.Hint == "" {
			t.Fatalf("%s envelope hint is empty", languageID)
		}
		if idx == 0 {
			baseline = envelope
			continue
		}
		requireEnvelopeMatchesBaseline(t, languageID, envelope, baseline)
	}

	schemaEnvelope := newToolErrorEnvelope("file", "python", errors.New("decode params: json: unknown field \"line\""))
	if schemaEnvelope.Code != "schema_invalid" {
		t.Fatalf("schema envelope code = %q, want schema_invalid", schemaEnvelope.Code)
	}
	if schemaEnvelope.Meta["language_id"] != "python" {
		t.Fatalf("schema envelope language_id = %#v, want python", schemaEnvelope.Meta["language_id"])
	}
}

func TestStructuredToolErrorEnvelopeIsLanguageAgnosticForGoPythonAndTypeScript(t *testing.T) {
	var baseline ToolErrorEnvelope
	for idx, languageID := range []string{"go", "python", "typescript"} {
		envelope := newToolErrorEnvelope("inspect", languageID, fmt.Errorf("%s route: %w", languageID, lspmanager.ErrUnsupportedLanguage))
		requireUnsupportedLanguageEnvelope(t, envelope, languageID)
		if idx == 0 {
			baseline = envelope
			continue
		}
		requireEnvelopeMatchesBaseline(t, languageID, envelope, baseline)
	}
}
