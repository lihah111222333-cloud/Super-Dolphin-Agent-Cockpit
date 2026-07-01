package systemlog

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestSQLiteSystemLogInsertAndListFilters(t *testing.T) {
	t.Parallel()

	db := openSystemLogSQLiteDB(t)
	s := NewStore(sqlc.New(db))

	duration := int32(87)
	if err := s.Insert(context.Background(), InsertParams{
		Level:        "warn",
		Logger:       "app",
		Message:      "inserted",
		Raw:          "heavy raw",
		Source:       "mcp-control",
		Component:    "mcp-lsp",
		AgentID:      "agent-1",
		ThreadID:     "thread-1",
		TraceID:      "trace-1",
		SpanID:       "span-1",
		ParentSpanID: "parent-1",
		EventType:    "ctl/log",
		ToolName:     "mcp-lsp",
		DurationMs:   &duration,
		Extra:        json.RawMessage(`{"span_id":"span-1"}`),
	}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	insertSystemLogFixture(t, db, 1_700_000_002_000, "info", "agent", "Hello Provider", "very large raw payload", "provider")

	var insertedTS int64
	var traceID, spanID, parentSpanID, source, component, extra string
	var storedDuration int64
	if err := db.QueryRow(`SELECT ts, trace_id, span_id, parent_span_id, source, component, duration_ms, extra FROM system_logs WHERE message = 'inserted'`).Scan(&insertedTS, &traceID, &spanID, &parentSpanID, &source, &component, &storedDuration, &extra); err != nil {
		t.Fatalf("read inserted ts: %v", err)
	}
	if insertedTS == 0 {
		t.Fatal("Insert() stored ts = 0, want Go epoch milliseconds")
	}
	assertInsertedSystemLogMetadata(t, traceID, spanID, parentSpanID, source, component, storedDuration, extra)

	rows, err := s.List(context.Background(), ListFilter{
		Level:        "info",
		Source:       "provider",
		TraceID:      "trace-fixture",
		SpanID:       "span-fixture",
		ParentSpanID: "parent-fixture",
		Keyword:      "hello",
		Limit:        10,
	})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	assertListedSystemLogTraceRow(t, rows)
}

func assertListedSystemLogTraceRow(t *testing.T, rows []SystemLog) {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("List() len = %d, want 1: %+v", len(rows), rows)
	}
	row := rows[0]
	if row.Raw != "very large raw payload" || string(row.Extra) != `{"large":"json"}` {
		t.Fatalf("List() raw/extra = raw=%q extra=%s", row.Raw, row.Extra)
	}
	if row.TraceID != "trace-fixture" || row.SpanID != "span-fixture" || row.ParentSpanID != "parent-fixture" {
		t.Fatalf("List() trace fields = trace:%q span:%q parent:%q", row.TraceID, row.SpanID, row.ParentSpanID)
	}
}

func assertInsertedSystemLogMetadata(t *testing.T, traceID, spanID, parentSpanID, source, component string, duration int64, extra string) {
	t.Helper()
	for _, check := range []struct{ name, got, want string }{
		{name: "trace_id", got: traceID, want: "trace-1"},
		{name: "span_id", got: spanID, want: "span-1"},
		{name: "parent_span_id", got: parentSpanID, want: "parent-1"},
		{name: "source", got: source, want: "mcp-control"},
		{name: "component", got: component, want: "mcp-lsp"},
		{name: "extra", got: extra, want: `{"span_id":"span-1"}`},
	} {
		if check.got != check.want {
			t.Fatalf("Insert() %s = %q, want %q", check.name, check.got, check.want)
		}
	}
	if duration != 87 {
		t.Fatalf("Insert() duration_ms = %d, want 87", duration)
	}
}

func openSystemLogSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec("PRAGMA foreign_keys = ON"); err != nil {
		t.Fatalf("enable foreign keys: %v", err)
	}
	body, err := os.ReadFile(filepath.Join("..", "..", "platform", "db", "sqlite", "migrations", "001_baseline.sql"))
	if err != nil {
		t.Fatalf("read baseline: %v", err)
	}
	if _, err := db.Exec(string(body)); err != nil {
		t.Fatalf("exec baseline: %v", err)
	}
	return db
}

func insertSystemLogFixture(t *testing.T, db *sql.DB, ts int64, level, logger, message, raw, source string) {
	t.Helper()

	_, err := db.Exec(`
INSERT INTO system_logs (
    ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, span_id, parent_span_id, event_type, tool_name, duration_ms, extra
) VALUES (?, ?, ?, ?, ?, ?, 'component', 'agent-1', 'thread-1', 'trace-fixture', 'span-fixture', 'parent-fixture', 'event', 'tool', 12, ?);
`, ts, level, logger, message, raw, source, `{"large":"json"}`)
	if err != nil {
		t.Fatalf("insert system log fixture: %v", err)
	}
}
