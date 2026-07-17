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

func TestSafeToolArgumentsPreviewRedactsExplicitSensitiveKeyVariants(t *testing.T) {
	preview := SafeToolArgumentsPreview(map[string]any{
		"privateKey":  "private-camel-leak",
		"private_key": "private-snake-leak",
		"credential":  "credential-leak",
		"cookie":      "cookie-leak",
		"session":     "session-leak",
		"certificate": "certificate-leak",
		"key":         "ordinary-key-value",
		"keyboard":    "ordinary-keyboard-value",
	})

	for _, fragment := range []string{
		"private-camel-leak",
		"private-snake-leak",
		"credential-leak",
		"cookie-leak",
		"session-leak",
		"certificate-leak",
	} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("SafeToolArgumentsPreview() = %q, must not contain %q", preview, fragment)
		}
	}
	for _, fragment := range []string{"ordinary-key-value", "ordinary-keyboard-value"} {
		if !strings.Contains(preview, fragment) {
			t.Fatalf("SafeToolArgumentsPreview() = %q, want ordinary field value %q", preview, fragment)
		}
	}
}

func TestSafeToolArgumentsPreviewRedactsPEMAndNestedJSONString(t *testing.T) {
	privateKeyPEM := "-----BEGIN PRIVATE KEY-----\nprivate-key-leak\n-----END PRIVATE KEY-----"
	certificatePEM := "-----BEGIN CERTIFICATE-----\ncertificate-pem-leak\n-----END CERTIFICATE-----"
	doubleEncoded := `{"payload":"{\"credential\":\"nested-credential-leak\"}"}`
	preview := SafeToolArgumentsPreview(map[string]any{
		"privateMaterial": privateKeyPEM,
		"certificatePEM":  certificatePEM,
		"envelope":        doubleEncoded,
	})

	for _, fragment := range []string{"private-key-leak", "certificate-pem-leak", "nested-credential-leak", "BEGIN PRIVATE KEY", "BEGIN CERTIFICATE"} {
		if strings.Contains(preview, fragment) {
			t.Fatalf("SafeToolArgumentsPreview() = %q, must not contain %q", preview, fragment)
		}
	}
	if !strings.Contains(preview, "[REDACTED]") {
		t.Fatalf("SafeToolArgumentsPreview() = %q, want redaction marker", preview)
	}
}

func TestSafeToolArgumentsPreviewStringFailsClosedOnPEM(t *testing.T) {
	for _, raw := range []string{
		"prefix -----BEGIN RSA PRIVATE KEY-----\nprivate-key-leak\n-----END RSA PRIVATE KEY----- suffix",
		"prefix -----BEGIN CERTIFICATE-----\ncertificate-leak\n-----END CERTIFICATE----- suffix",
	} {
		if preview := SafeToolArgumentsPreviewString(raw); preview != "[REDACTED]" {
			t.Fatalf("SafeToolArgumentsPreviewString() = %q, want fail-closed redaction", preview)
		}
	}
}
