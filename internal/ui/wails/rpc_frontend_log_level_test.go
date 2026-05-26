package wails

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestUILogRouteDemotesSidebarRefreshLifecycleWarnings(t *testing.T) {
	var logs bytes.Buffer
	origLogger := pkglogger.Get()
	pkglogger.SetForTest(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { pkglogger.SetForTest(origLogger) })

	server := newWailsRPCServer(t, &App{})
	_, err := server.Dispatch(context.Background(), "ui/log", json.RawMessage(`{
		"entries":[
			{"level":"warn","scope":"thread","event":"sidebar.refresh.api_call_done","seq":1},
			{"level":"warn","scope":"thread","event":"sidebar.refresh.failed","seq":2}
		],
		"_aoClientKind":"web-debug-shim",
		"_aoClientRoute":"/"
	}`))
	if err != nil {
		t.Fatalf("Dispatch(ui/log) error = %v", err)
	}

	records := parseJSONLogRecords(t, logs.String())
	done := findFrontendLogRecord(t, records, "sidebar.refresh.api_call_done")
	if got := done["level"]; got != "DEBUG" {
		t.Fatalf("api_call_done slog level = %v, want DEBUG", got)
	}
	if got := done["frontend_level"]; got != "debug" {
		t.Fatalf("api_call_done frontend_level = %v, want debug", got)
	}
	failed := findFrontendLogRecord(t, records, "sidebar.refresh.failed")
	if got := failed["level"]; got != "WARN" {
		t.Fatalf("failed slog level = %v, want WARN", got)
	}
	if got := failed["frontend_level"]; got != "warn" {
		t.Fatalf("failed frontend_level = %v, want warn", got)
	}
}

func parseJSONLogRecords(t *testing.T, raw string) []map[string]any {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	records := make([]map[string]any, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("Unmarshal log line %q: %v", line, err)
		}
		records = append(records, record)
	}
	return records
}

func findFrontendLogRecord(t *testing.T, records []map[string]any, event string) map[string]any {
	t.Helper()
	for _, record := range records {
		if record["frontend_event"] == event {
			return record
		}
	}
	t.Fatalf("missing frontend log event %q in %#v", event, records)
	return nil
}
