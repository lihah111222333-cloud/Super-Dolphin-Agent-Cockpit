package shared

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestSafeRuntimeLogFieldsRedactsCWD(t *testing.T) {
	rawCWD := "/Users/example/private/repo"

	fields := SafeRuntimeLogFields("cwd", rawCWD)
	rendered := fmt.Sprint(fields)

	if strings.Contains(rendered, rawCWD) {
		t.Fatalf("SafeRuntimeLogFields exposed raw cwd in %q", rendered)
	}
	assertSafeStringField(t, fields, "cwd_display_class", "absolute_path")
	if hash := safeSummaryString(t, fields, "cwd_sha256"); len(hash) != 64 {
		t.Fatalf("cwd_sha256 length = %d, want 64", len(hash))
	}

	homeFields := SafeRuntimeLogFields("codexHome", "/Users/example/.codex")
	if rendered := fmt.Sprint(homeFields); strings.Contains(rendered, "/Users/example/.codex") {
		t.Fatalf("SafeRuntimeLogFields exposed raw provider home in %q", rendered)
	}
	assertSafeStringField(t, homeFields, "codexHome_display_class", "absolute_path")
}

func TestRuntimeLogFieldsDoNotExposeRawValues(t *testing.T) {
	rawPath := "/Users/example/private/repo/.codex/history.jsonl"
	rawPayload := json.RawMessage(`{"prompt":"secret prompt","config":{"token":"secret-token"},"sandbox_policy":"danger-full-access"}`)
	rawInstructions := "do not leak this instruction body"

	fields := append([]any{}, SafePathLogFields("rollout_path", rawPath)...)
	fields = append(fields, SafePayloadLogFields("payload", rawPayload)...)
	fields = append(fields, SafeRuntimeLogFields(
		"prompt", "secret prompt",
		"config_body", rawPayload,
		"instructions", rawInstructions,
		"sandbox_policy", "danger-full-access",
	)...)
	rendered := fmt.Sprint(fields)

	for _, raw := range []string{rawPath, string(rawPayload), "secret prompt", "secret-token", rawInstructions, "danger-full-access"} {
		if strings.Contains(rendered, raw) {
			t.Fatalf("safe runtime fields exposed raw value %q in %q", raw, rendered)
		}
	}
	for _, key := range []string{"rollout_path_sha256", "payload_sha256", "prompt_sha256", "config_body_sha256", "instructions_sha256", "sandbox_policy_sha256"} {
		if hash := safeSummaryString(t, fields, key); len(hash) != 64 {
			t.Fatalf("%s length = %d, want 64", key, len(hash))
		}
	}
}

func safeSummaryString(t *testing.T, fields []any, key string) string {
	t.Helper()
	for i := 0; i+1 < len(fields); i += 2 {
		if fields[i] == key {
			value, ok := fields[i+1].(string)
			if !ok {
				t.Fatalf("%s value = %T %#v, want string", key, fields[i+1], fields[i+1])
			}
			return value
		}
	}
	t.Fatalf("missing field %s in %#v", key, fields)
	return ""
}

func assertSafeStringField(t *testing.T, fields []any, key, want string) {
	t.Helper()
	got := safeSummaryString(t, fields, key)
	if got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}
