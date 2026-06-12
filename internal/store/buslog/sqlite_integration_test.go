package buslog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestSQLiteBusLogListFiltersAndMetadataProjection(t *testing.T) {
	t.Parallel()

	db := openBusLogSQLiteDB(t)
	insertBusLogFixture(t, db, 1_700_000_002_000, "rpc", "error", "dashboard", "task_get", "failed hard", "STACK payload")
	insertBusLogFixture(t, db, 1_700_000_001_000, "bus", "warn", "cron", "task_run", "stale", "other")

	s := NewStore(sqlc.New(db))
	rows, err := s.List(context.Background(), ListFilter{Category: "rpc", Severity: "error", Keyword: "stack", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 1 || rows[0].ID == 0 || rows[0].Category != "rpc" {
		t.Fatalf("List() = %+v", rows)
	}
	if rows[0].Traceback != "" || string(rows[0].Extra) != `{}` {
		t.Fatalf("List() projected heavy fields: traceback=%q extra=%s", rows[0].Traceback, rows[0].Extra)
	}
}

func openBusLogSQLiteDB(t *testing.T) *sql.DB {
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

func insertBusLogFixture(t *testing.T, db *sql.DB, ts int64, category, severity, source, tool, message, traceback string) {
	t.Helper()

	_, err := db.Exec(`
INSERT INTO bus_exception_logs (ts, category, severity, source, tool_name, message, traceback, extra)
VALUES (?, ?, ?, ?, ?, ?, ?, ?);
`, ts, category, severity, source, tool, message, traceback, `{"large":"bus"}`)
	if err != nil {
		t.Fatalf("insert bus log fixture: %v", err)
	}
}
