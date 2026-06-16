package startconfig

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestSanitizeSandboxTrimsAndRejectsInvalidJSON verifies sandbox payloads are valid JSON before use.
func TestSanitizeSandboxTrimsAndRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	got, err := SanitizeSandbox(json.RawMessage("  {\"type\":\"read-only\"}  "))
	if err != nil {
		t.Fatalf("SanitizeSandbox() error = %v", err)
	}
	if string(got) != "{\"type\":\"read-only\"}" {
		t.Fatalf("SanitizeSandbox() = %s, want trimmed object", got)
	}

	if got, err := SanitizeSandbox(nil); err != nil || got != nil {
		t.Fatalf("SanitizeSandbox(nil) = %s, %v; want nil, nil", got, err)
	}

	if _, err := SanitizeSandbox(json.RawMessage("{")); err == nil || !strings.Contains(err.Error(), "invalid sandbox JSON") {
		t.Fatalf("SanitizeSandbox(invalid) error = %v, want invalid JSON error", err)
	}
}

// TestIsDangerFullAccessSandboxAcceptsStringAndObjectForms verifies supported sandbox encodings.
func TestIsDangerFullAccessSandboxAcceptsStringAndObjectForms(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{name: "string hyphenated", raw: json.RawMessage(`"danger-full-access"`), want: true},
		{name: "object underscored", raw: json.RawMessage(`{"type":"danger_full_access"}`), want: true},
		{name: "read only", raw: json.RawMessage(`{"type":"read-only"}`), want: false},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := IsDangerFullAccessSandbox(tt.raw)
			if err != nil {
				t.Fatalf("IsDangerFullAccessSandbox() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("IsDangerFullAccessSandbox() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestIsDangerFullAccessSandboxRejectsMalformedObjects verifies malformed sandbox objects fail closed.
func TestIsDangerFullAccessSandboxRejectsMalformedObjects(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{name: "missing type", raw: json.RawMessage(`{"other":"x"}`), want: "type is required"},
		{name: "non string type", raw: json.RawMessage(`{"type":7}`), want: "invalid sandbox type"},
		{name: "non string array", raw: json.RawMessage(`[]`), want: "expected string or object"},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := IsDangerFullAccessSandbox(tt.raw)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("IsDangerFullAccessSandbox() error = %v, want %q", err, tt.want)
			}
		})
	}
}
