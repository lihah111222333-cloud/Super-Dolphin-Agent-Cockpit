package shared

import (
	"strings"
	"testing"
)

func TestSafeToolArgumentsPreviewRedactsSensitiveFragments(t *testing.T) {
	preview := SafeToolArgumentsPreview(map[string]any{
		"command":   "curl --api-key sk-test https://example.test",
		"token":     "token=abc",
		"file_path": "/Users/alice/secret",
		"env":       map[string]any{"OPENAI_API_KEY": "sk-test"},
	})

	for _, fragment := range []string{"token=abc", "sk-test", "/Users/alice/secret", "--api-key"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("SafeToolArgumentsPreview() = %q, must not contain %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("SafeToolArgumentsPreview() = %q, want redaction marker", preview)
	}
}

func TestSafeToolArgumentsPreviewRedactsNestedJSONStringFields(t *testing.T) {
	preview := SafeToolArgumentsPreview(map[string]any{
		"contentItems": []map[string]any{{
			"text": `{"success":true,"path":"/Users/alice/secret/smoke.go","api_key":"sk-test"}`,
		}},
	})

	for _, fragment := range []string{"/Users/alice/secret/smoke.go", "sk-test"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("SafeToolArgumentsPreview() = %q, must not contain %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("SafeToolArgumentsPreview() = %q, want redaction marker", preview)
	}
}

func TestSafeToolArgumentsPreviewCapsRawBeforeProcessingAndOutput(t *testing.T) {
	raw := strings.Repeat("a", 16*1024+2048) + "/Users/alice/secret"
	preview := SafeToolArgumentsPreviewString(raw)

	if len(preview) > 512 {
		t.Fatalf("preview length = %d, want <= 512", len(preview))
	}
	if !strings.HasSuffix(preview, "... [truncated]") {
		t.Fatalf("preview = %q, want truncation suffix", preview)
	}
	if strings.Contains(preview, "/Users/alice/secret") {
		t.Fatalf("preview = %q, must not contain path beyond raw cap", preview)
	}
}
