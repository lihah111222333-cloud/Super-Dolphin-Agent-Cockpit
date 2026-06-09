package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestFromContextAddsTraceFields(t *testing.T) {
	var buf bytes.Buffer
	SetForTest(slog.New(newHandler(Production, slog.LevelInfo, &buf)))
	t.Cleanup(func() { SetForTest(newLogger(activeMode, activeLevel)) })

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

func TestDebugLogLevelKeepsJSONMode(t *testing.T) {
	mode, level := resolveInitModeAndLevel("debug")
	if mode != Production {
		t.Fatalf("mode = %q, want production JSON mode", mode)
	}
	if level != slog.LevelDebug {
		t.Fatalf("level = %v, want debug", level)
	}
}
