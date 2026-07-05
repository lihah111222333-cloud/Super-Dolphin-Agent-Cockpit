package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"
)

func TestFromContextAddsTraceFields(t *testing.T) {
	var buf bytes.Buffer
	previous := InstallRuntime(NewRuntime(RuntimeConfig{}))
	SetForTest(slog.New(newHandler(Production, slog.LevelInfo, &buf)))
	t.Cleanup(func() { InstallRuntime(previous) })

	ctx := WithTraceContext(context.Background(), "trace-1", "span-1", "parent-1")
	FromContext(ctx).Info("hello")

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected log output")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("unmarshal log output: %v", err)
	}
	if got := payload[FieldECSTraceID]; got != "trace-1" {
		t.Fatalf("trace.id = %#v, want trace-1", got)
	}
	if got := payload[FieldECSSpanID]; got != "span-1" {
		t.Fatalf("span.id = %#v, want span-1", got)
	}
	if got := payload[FieldECSParentSpanID]; got != "parent-1" {
		t.Fatalf("span.parent_id = %#v, want parent-1", got)
	}
}

func TestExtractTraceCarrierFieldsDerivesTraceparent(t *testing.T) {
	trace, err := ExtractTraceCarrierFields(map[string]any{
		FieldAOTraceparent: "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		FieldParentSpanID:  "parent-1",
	}, TraceFieldAliases{})
	if err != nil {
		t.Fatalf("ExtractTraceCarrierFields() error = %v", err)
	}
	if trace.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" ||
		trace.SpanID != "00f067aa0ba902b7" ||
		trace.ParentSpanID != "parent-1" {
		t.Fatalf("trace = %+v, want traceparent ids with parent span", trace)
	}
}

func TestExtractTraceCarrierFieldsRejectsTraceparentMismatch(t *testing.T) {
	_, err := ExtractTraceCarrierFields(map[string]any{
		FieldTraceID:     "11111111111111111111111111111111",
		FieldTraceparent: "00-22222222222222222222222222222222-3333333333333333-01",
	}, DefaultTraceFieldAliases())
	if err == nil || !strings.Contains(err.Error(), "trace_id does not match traceparent") {
		t.Fatalf("ExtractTraceCarrierFields() error = %v, want trace_id mismatch", err)
	}
}

func TestExtractAOTraceCarrierJSONRejectsMismatchedMetadata(t *testing.T) {
	trace, ok, err := ExtractAOTraceCarrierJSON(map[string]json.RawMessage{
		FieldAOTraceparent: json.RawMessage(`"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"`),
		FieldAOTraceID:     json.RawMessage(`"4bf92f3577b34da6a3ce929d0e0e4736"`),
		FieldAOSpanID:      json.RawMessage(`"00f067aa0ba902b7"`),
	})
	if err != nil || !ok {
		t.Fatalf("ExtractAOTraceCarrierJSON() = (%+v, %v, %v), want trace and ok", trace, ok, err)
	}
	if trace.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" || trace.SpanID != "00f067aa0ba902b7" {
		t.Fatalf("trace = %+v, want traceparent ids", trace)
	}

	_, _, err = ExtractAOTraceCarrierJSON(map[string]json.RawMessage{
		FieldAOTraceparent: json.RawMessage(`"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"`),
		FieldAOTraceID:     json.RawMessage(`"11111111111111111111111111111111"`),
	})
	if err == nil || !strings.Contains(err.Error(), "mismatched _aoTraceId") {
		t.Fatalf("ExtractAOTraceCarrierJSON() error = %v, want mismatched _aoTraceId", err)
	}
}

func TestParseTraceparentRejectsInvalidParts(t *testing.T) {
	cases := map[string]string{
		"short fields":    "not-a-traceparent",
		"version":         "01-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01",
		"all zero trace":  "00-00000000000000000000000000000000-00f067aa0ba902b7-01",
		"all zero span":   "00-4bf92f3577b34da6a3ce929d0e0e4736-0000000000000000-01",
		"uppercase trace": "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01",
		"flags":           "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-zz",
	}
	for name, traceparent := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTraceparent(traceparent); err == nil {
				t.Fatal("ParseTraceparent() error = nil, want rejection")
			}
		})
	}
}

func TestTraceIdentifierValidators(t *testing.T) {
	if !ValidTraceID("4bf92f3577b34da6a3ce929d0e0e4736") {
		t.Fatal("ValidTraceID() = false, want true")
	}
	if ValidTraceID("00000000000000000000000000000000") {
		t.Fatal("ValidTraceID(all zero) = true, want false")
	}
	if !ValidSpanID("00f067aa0ba902b7") {
		t.Fatal("ValidSpanID() = false, want true")
	}
	if ValidSpanID("0000000000000000") {
		t.Fatal("ValidSpanID(all zero) = true, want false")
	}
	if !ValidTraceFlags("01") {
		t.Fatal("ValidTraceFlags() = false, want true")
	}
	if ValidTraceFlags("0G") {
		t.Fatal("ValidTraceFlags(uppercase/non-hex) = true, want false")
	}
}

func TestExtractTraceCarrierFieldsRejectsUnsafeToken(t *testing.T) {
	_, err := ExtractTraceCarrierFields(map[string]any{
		FieldSpanID: "span/unsafe",
	}, DefaultTraceFieldAliases())
	if err == nil || !strings.Contains(err.Error(), "span_id contains unsafe characters") {
		t.Fatalf("ExtractTraceCarrierFields() error = %v, want unsafe span token", err)
	}
}

func TestProductionErrorLogAddsErrorFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(Production, slog.LevelInfo, &buf))
	logger.Error("boom")

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected log output")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("unmarshal log output: %v", err)
	}
	if _, ok := payload[FieldSource]; !ok {
		t.Fatalf("missing %s in %#v", FieldSource, payload)
	}
	if _, ok := payload[FieldFunction]; !ok {
		t.Fatalf("missing %s in %#v", FieldFunction, payload)
	}
	if _, ok := payload[FieldECSErrorStackTrace]; !ok {
		t.Fatalf("missing %s in %#v", FieldECSErrorStackTrace, payload)
	}
}

func TestProductionInfoLogDoesNotAddStacktrace(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(Production, slog.LevelInfo, &buf))
	logger.Info("ok")

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected log output")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("unmarshal log output: %v", err)
	}
	if _, ok := payload[FieldECSErrorStackTrace]; ok {
		t.Fatalf("did not expect %s in %#v", FieldECSErrorStackTrace, payload)
	}
	if _, ok := payload[FieldFunction]; ok {
		t.Fatalf("did not expect %s in %#v", FieldFunction, payload)
	}
}

func TestProductionLogUsesECSCoreFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(Production, slog.LevelInfo, &buf))
	logger.Info("ok")

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected log output")
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("unmarshal log output: %v", err)
	}
	if payload[FieldTimestamp] == nil {
		t.Fatalf("missing %s in %#v", FieldTimestamp, payload)
	}
	if got := payload[FieldLogLevel]; got != "info" {
		t.Fatalf("log.level = %#v, want info", got)
	}
	if got := payload["message"]; got != "ok" {
		t.Fatalf("message = %#v, want ok", got)
	}
}

func TestReplaceLogAttrFormatsTimestampAsUTCPlus8DateTime(t *testing.T) {
	stamp := time.Date(2026, 6, 15, 1, 2, 3, 456_000_000, time.UTC)

	got := replaceLogAttr(nil, slog.Time(slog.TimeKey, stamp))

	if got.Key != FieldTimestamp {
		t.Fatalf("timestamp key = %q, want %q", got.Key, FieldTimestamp)
	}
	if got.Value.String() != "2026-06-15 09:02:03" {
		t.Fatalf("timestamp = %q, want UTC+8 yyyy-MM-dd HH:mm:ss", got.Value.String())
	}
}

func TestProductionLogRedactsSensitiveFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(Production, slog.LevelInfo, &buf))
	logger.Info("secret check",
		"api_key", "sk-123456789",
		"details", "Authorization: Bearer abc.def token=plain",
	)

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected log output")
	}
	if strings.Contains(output, "sk-123456789") || strings.Contains(output, "abc.def") || strings.Contains(output, "token=plain") {
		t.Fatalf("log output leaked secret: %s", output)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("unmarshal log output: %v", err)
	}
	if got := payload["api_key"]; got != redactedValue {
		t.Fatalf("api_key = %#v, want redacted", got)
	}
}

func TestProductionLogRedactsDatabaseConfigFields(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(newHandler(Production, slog.LevelInfo, &buf))
	resolvedPath := "/Users/alice/Library/Application Support/Super Dolphin/super-dolphin.db"
	walPath := resolvedPath + "-wal"
	logger.Info("config dump",
		"database_url", "postgres://alice:secret@127.0.0.1:5432/super_dolphin?sslmode=disable",
		"postgres_connection_string", "postgres://compat:secret@127.0.0.1:5432/super_dolphin?sslmode=disable",
		"sqlite_path", resolvedPath,
		"internal_sqlite_path", resolvedPath,
		"details", "DATABASE_URL=postgres://alice:secret@127.0.0.1:5432/super_dolphin POSTGRES_CONNECTION_STRING=postgres://compat:secret@127.0.0.1:5432/super_dolphin SUPER_DOLPHIN_SQLITE_PATH="+resolvedPath+" SUPER_DOLPHIN_INTERNAL_SQLITE_PATH="+resolvedPath+" wal="+walPath,
	)

	output := strings.TrimSpace(buf.String())
	if output == "" {
		t.Fatal("expected log output")
	}
	for _, forbidden := range []string{
		"alice:secret",
		"compat:secret",
		resolvedPath,
		walPath,
	} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("log output leaked %q: %s", forbidden, output)
		}
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		t.Fatalf("unmarshal log output: %v", err)
	}
	for _, key := range []string{"database_url", "postgres_connection_string", "sqlite_path", "internal_sqlite_path"} {
		if got := payload[key]; got != redactedValue {
			t.Fatalf("%s = %#v, want redacted", key, got)
		}
	}
}

func TestDebugLogLevelKeepsJSONMode(t *testing.T) {
	mode, level := resolveInitModeAndLevel("debug")
	if mode != Production {
		t.Fatalf("mode = %q, want production JSON mode", mode)
	}
	if level != slog.LevelDebug {
		t.Fatalf("level = %v, want debug", level)
	}
}
