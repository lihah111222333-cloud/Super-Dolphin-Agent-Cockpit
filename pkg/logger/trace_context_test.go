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
	SetForTest(slog.New(slog.NewJSONHandler(&buf, nil)))
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
	if got := payload[FieldTraceID]; got != "trace-1" {
		t.Fatalf("trace_id = %#v, want trace-1", got)
	}
	if got := payload[FieldSpanID]; got != "span-1" {
		t.Fatalf("span_id = %#v, want span-1", got)
	}
	if got := payload[FieldParentSpanID]; got != "parent-1" {
		t.Fatalf("parent_span_id = %#v, want parent-1", got)
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
	if _, ok := payload[FieldStacktrace]; !ok {
		t.Fatalf("missing %s in %#v", FieldStacktrace, payload)
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
	if _, ok := payload[FieldStacktrace]; ok {
		t.Fatalf("did not expect %s in %#v", FieldStacktrace, payload)
	}
	if _, ok := payload[FieldFunction]; ok {
		t.Fatalf("did not expect %s in %#v", FieldFunction, payload)
	}
}
