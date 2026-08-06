package wails

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"

	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
)

func TestUILogRouteDemotesSidebarRefreshLifecycleWarnings(t *testing.T) {
	var logs bytes.Buffer
	origLogger := slog.Default()
	loggerRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	loggerRuntime.SetForTest(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	loggerRuntime.BindDefault()
	t.Cleanup(func() { slog.SetDefault(origLogger) })

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

func TestUILogRouteRedactsNestedFrontendFieldsInLogsAndRelay(t *testing.T) {
	payload := json.RawMessage(`{
		"entries":[{
			"level":"warn",
			"scope":"auth",
			"event":"login_failed",
			"seq":7,
			"fields":{
				"Authorization":"Bearer raw-auth-token",
				"details":{
					"token":"raw-nested-token",
					"cookie":"raw-nested-cookie",
					"note":"password=raw-inline-password Authorization: Bearer raw-inline-auth"
				},
				"attempts":[
					{"password":"raw-list-password"},
					"cookie=raw-inline-cookie"
				]
			}
		}],
		"_aoClientKind":"desktop-wails",
		"_aoClientRoute":"/login"
	}`)
	forbidden := []string{
		"raw-auth-token",
		"raw-nested-token",
		"raw-nested-cookie",
		"raw-inline-password",
		"raw-inline-auth",
		"raw-list-password",
		"raw-inline-cookie",
	}

	origLogger := slog.Default()
	loggerRuntime := pkglogger.NewRuntime(pkglogger.RuntimeConfig{})
	t.Cleanup(func() {
		loggerRuntime.ClearRelayHook()
		slog.SetDefault(origLogger)
	})

	var logs bytes.Buffer
	loggerRuntime.SetForTest(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	loggerRuntime.BindDefault()
	server := newWailsRPCServer(t, &App{})
	if _, err := server.Dispatch(context.Background(), "ui/log", payload); err != nil {
		t.Fatalf("Dispatch(ui/log) for local log error = %v", err)
	}
	assertNoFrontendLogLeak(t, logs.String(), forbidden)

	var relays []pkglogger.RelayPayload
	loggerRuntime.InitWithConsoleWriter(io.Discard)
	loggerRuntime.SetRelayHook(func(_ context.Context, payload pkglogger.RelayPayload) {
		if payload.Msg == "frontend: auth.login_failed" {
			relays = append(relays, payload)
		}
	})
	if _, err := server.Dispatch(context.Background(), "ui/log", payload); err != nil {
		t.Fatalf("Dispatch(ui/log) for relay error = %v", err)
	}
	if len(relays) == 0 {
		t.Fatal("relay payloads len = 0, want frontend log relay payload")
	}
	encodedRelay, err := json.Marshal(relays)
	if err != nil {
		t.Fatalf("Marshal relay payloads error = %v", err)
	}
	assertNoFrontendLogLeak(t, string(encodedRelay), forbidden)
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

func assertNoFrontendLogLeak(t *testing.T, raw string, forbidden []string) {
	t.Helper()
	if !strings.Contains(raw, "[REDACTED]") {
		t.Fatalf("log output missing redaction marker: %s", raw)
	}
	for _, value := range forbidden {
		if strings.Contains(raw, value) {
			t.Fatalf("log output leaked %q: %s", value, raw)
		}
	}
}
