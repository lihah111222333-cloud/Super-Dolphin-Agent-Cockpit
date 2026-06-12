package systemlog

import (
	"context"
	"database/sql"
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

	if err := s.Insert(context.Background(), InsertParams{Level: "warn", Logger: "app", Message: "inserted", Raw: "heavy raw"}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	insertSystemLogFixture(t, db, 1_700_000_002_000, "info", "agent", "Hello Provider", "very large raw payload", "provider")

	var insertedTS int64
	if err := db.QueryRow(`SELECT ts FROM system_logs WHERE message = 'inserted'`).Scan(&insertedTS); err != nil {
		t.Fatalf("read inserted ts: %v", err)
	}
	if insertedTS == 0 {
		t.Fatal("Insert() stored ts = 0, want Go epoch milliseconds")
	}

	rows, err := s.List(context.Background(), ListFilter{Level: "info", Source: "provider", Keyword: "hello", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("List() len = %d, want 1: %+v", len(rows), rows)
	}
	if rows[0].Raw != "" || string(rows[0].Extra) != `{}` {
		t.Fatalf("List() projected heavy fields: raw=%q extra=%s", rows[0].Raw, rows[0].Extra)
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
    ts, level, logger, message, raw, source, component, agent_id, thread_id, trace_id, event_type, tool_name, duration_ms, extra
) VALUES (?, ?, ?, ?, ?, ?, 'component', 'agent-1', 'thread-1', 'trace-1', 'event', 'tool', 12, ?);
`, ts, level, logger, message, raw, source, `{"large":"json"}`)
	if err != nil {
		t.Fatalf("insert system log fixture: %v", err)
	}
}
