package observability

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestJSONLSinkAppendsSingleLineEvents(t *testing.T) {
	cfg := enabledTestConfig(t)
	dir := t.TempDir()
	sink, err := NewJSONLSinkInDir(dir, cfg)
	if err != nil {
		t.Fatalf("NewJSONLSinkInDir: %v", err)
	}
	sink.now = func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }
	defer sink.Close()

	appendTraceEvent(t, sink, TraceEvent{TraceID: "trace-1", Status: StatusOK, Metadata: map[string]any{"k": "v"}})
	appendTraceEvent(t, sink, TraceEvent{TraceID: "trace-2", Status: StatusError, Error: "token=secret"})
	closeTraceSink(t, sink)

	data := readTraceFile(t, dir, "trace-2026-06-01.jsonl")
	assertJSONLLines(t, data, 2)
	assertNoSubstring(t, string(data), "secret")
	if stats := sink.Stats(); stats.EventsWritten != 2 || stats.WriteErrors != 0 {
		t.Fatalf("Stats = %+v, want 2 writes and 0 errors", stats)
	}
}

func TestTraceDirectoryUsesProjectTracePath(t *testing.T) {
	dir, err := TraceDirectory("demo-project")
	if err != nil {
		t.Fatalf("TraceDirectory: %v", err)
	}
	wantSuffix := filepath.Join(".super-dolphin", "log", "demo-project", "traces")
	if !strings.HasSuffix(dir, wantSuffix) {
		t.Fatalf("TraceDirectory = %q, want suffix %q", dir, wantSuffix)
	}
	if _, err := TraceDirectory("../escape"); err == nil {
		t.Fatalf("TraceDirectory accepted path traversal project")
	}
}

func TestJSONLSinkRotatesByDayAndSize(t *testing.T) {
	cfg := enabledTestConfig(t)
	cfg.JSONLMaxFileMB = 1
	dir := t.TempDir()
	sink, err := NewJSONLSinkInDir(dir, cfg)
	if err != nil {
		t.Fatalf("NewJSONLSinkInDir: %v", err)
	}
	now := time.Date(2026, 6, 3, 9, 0, 0, 0, time.UTC)
	sink.now = func() time.Time { return now }
	appendLargeTraceEvents(t, sink, cfg.StringMaxBytes, 5000)
	now = time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	appendTraceEvent(t, sink, TraceEvent{TraceID: "next-day", Status: StatusOK})
	closeTraceSink(t, sink)
	assertRotatedTraceFile(t, dir)
	assertExists(t, filepath.Join(dir, "trace-2026-06-04.jsonl"))
}

func appendTraceEvent(t *testing.T, sink *JSONLSink, event TraceEvent) {
	t.Helper()
	if err := sink.Append(context.Background(), event); err != nil {
		t.Fatalf("Append: %v", err)
	}
}

func closeTraceSink(t *testing.T, sink *JSONLSink) {
	t.Helper()
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func readTraceFile(t *testing.T, dir, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", name, err)
	}
	return data
}

func assertJSONLLines(t *testing.T, data []byte, want int) {
	t.Helper()
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	if len(lines) != want {
		t.Fatalf("line count = %d, want %d: %q", len(lines), want, data)
	}
	for _, line := range lines {
		assertTraceJSONLine(t, line)
	}
}

func assertTraceJSONLine(t *testing.T, line string) {
	t.Helper()
	if strings.Contains(line, "\n") || strings.TrimSpace(line) == "" {
		t.Fatalf("bad JSONL line %q", line)
	}
	var event TraceEvent
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		t.Fatalf("invalid JSON line %q: %v", line, err)
	}
	if event.SchemaVersion != SchemaVersion {
		t.Fatalf("schema_version = %d, want %d", event.SchemaVersion, SchemaVersion)
	}
}

func assertNoSubstring(t *testing.T, text, substring string) {
	t.Helper()
	if strings.Contains(text, substring) {
		t.Fatalf("text unexpectedly contains %q: %s", substring, text)
	}
}

func appendLargeTraceEvents(t *testing.T, sink *JSONLSink, stringMaxBytes int, count int) {
	t.Helper()
	large := strings.Repeat("x", stringMaxBytes)
	for i := range count {
		if err := sink.Append(context.Background(), TraceEvent{TraceID: "trace", SpanID: large, Status: StatusOK}); err != nil {
			t.Fatalf("Append large %d: %v", i, err)
		}
	}
}

func assertRotatedTraceFile(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "trace-2026-06-03-") && strings.HasSuffix(entry.Name(), ".jsonl") {
			return
		}
	}
	t.Fatalf("size rotation file not found in %#v", entryNames(entries))
}

func enabledTestConfig(t *testing.T) Config {
	t.Helper()
	cfg, err := ParseConfig(EnvMap{"OBS_TRACING_ENABLED": "1"})
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	return cfg
}

func assertPerm(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(%s): %v", path, err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s perm = %#o, want %#o", path, got, want)
	}
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
