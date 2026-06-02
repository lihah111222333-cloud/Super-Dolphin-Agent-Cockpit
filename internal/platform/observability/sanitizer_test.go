package observability

import (
	"math"
	"strings"
	"testing"
)

func TestSanitizerRedactsNormalizesAndTruncatesAllStringFields(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1", "OBS_EVENT_MAX_BYTES": "1024", "OBS_METADATA_MAX_BYTES": "256"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	s := NewSanitizer(cfg)
	event := TraceEvent{
		SchemaVersion: SchemaVersion,
		TraceID:       "trace\nwith token=abc123",
		SpanID:        strings.Repeat("s", 900),
		ParentSpanID:  "parent api_key=secret",
		Kind:          "rpc\nkind",
		Phase:         "phase\r\nnext",
		Method:        "Authorization: Bearer abc.def.ghi",
		ThreadID:      "thread password=hunter2",
		AgentID:       "agent sk-1234567890abcdef",
		TurnID:        "turn",
		CallID:        "call",
		ToolName:      "tool\nname",
		ClientKind:    "client",
		ClientRoute:   "/route?token=secret",
		Status:        StatusError,
		Error:         "line1\nline2 secret_key: value",
		Code:          CodeAnchor{File: "file\n.go?token=x", Function: "fn\nname", Line: 7},
		Stack:         []StackFrame{{File: "stack\n.go", Function: "stackfn password=p", Line: 9}},
		Metadata: map[string]any{
			"token":       "abc123",
			"api-key":     "PLAINSECRET",
			"multi":       "a\nb",
			"strings":     []string{"ok", "api_key=secret"},
			"ints":        []int64{1, 2},
			"nested":      map[string]string{"authorization": "Bearer secret", "ok": "value"},
			"bad_nested":  map[string]any{"drop": "me"},
			"not_finite":  math.Inf(1),
			"unsupported": []int{1, 2},
		},
	}

	got := s.SanitizeEvent(event)
	encoded := mustJSON(t, got)
	assertNoSecretLeak(t, encoded)
	assertSingleLine(t, encoded)
	if len(got.SpanID) > cfg.StringMaxBytes {
		t.Fatalf("SpanID length = %d, want <= %d", len(got.SpanID), cfg.StringMaxBytes)
	}
	if got.Metadata["metadata_dropped"] != true {
		t.Fatalf("metadata_dropped marker missing: %#v", got.Metadata)
	}
	if _, ok := got.Metadata["bad_nested"]; ok {
		t.Fatalf("unsupported nested metadata was kept: %#v", got.Metadata)
	}
}

func TestSanitizerMetadataAllowedShapes(t *testing.T) {
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	s := NewSanitizer(cfg)
	got := s.SanitizeMetadata(map[string]any{
		"string": "value",
		"bool":   true,
		"int":    int64(42),
		"float":  3.5,
		"list_s": []string{"a", "b"},
		"list_i": []int64{1, 2},
		"map":    map[string]string{"k": "v"},
	})
	for _, key := range []string{"string", "bool", "int", "float", "list_s", "list_i", "map"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("metadata key %q missing from %#v", key, got)
		}
	}
	if got["metadata_dropped"] == true {
		t.Fatalf("metadata_dropped set for allowed shapes: %#v", got)
	}
}

func assertNoSecretLeak(t *testing.T, encoded string) {
	t.Helper()
	for _, secret := range []string{"abc123", "hunter2", "Bearer secret", "sk-123", "PLAINSECRET"} {
		if strings.Contains(encoded, secret) {
			t.Fatalf("sanitized event leaked secret %q: %s", secret, encoded)
		}
	}
}

func assertSingleLine(t *testing.T, encoded string) {
	t.Helper()
	if strings.Contains(encoded, "\n") || strings.Contains(encoded, "\r") {
		t.Fatalf("sanitized event contains raw multiline text: %q", encoded)
	}
}

func mustJSON(t *testing.T, event TraceEvent) string {
	t.Helper()
	data, err := MarshalSanitizedJSON(event)
	if err != nil {
		t.Fatalf("MarshalSanitizedJSON: %v", err)
	}
	return string(data)
}
