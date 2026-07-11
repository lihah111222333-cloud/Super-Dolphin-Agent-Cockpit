package buslog

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestSQLiteBusLogListFiltersAndMetadataProjection(t *testing.T) {
	t.Parallel()

	db := openBusLogSQLiteDB(t)
	insertBusLogFixture(t, db, 1_700_000_002_000, "rpc", "error", "dashboard", "task_get", "failed hard", "STACK payload")
	insertBusLogFixture(t, db, 1_700_000_001_000, "bus", "warn", "cron", "task_run", "stale", "other")

	s := NewStore(sqlc.New(db))
	tracebackRows, err := s.List(context.Background(), ListFilter{Category: "rpc", Severity: "error", Keyword: "stack", Limit: 10})
	if err != nil {
		t.Fatalf("List(traceback keyword) error = %v", err)
	}
	if len(tracebackRows) != 0 {
		t.Fatalf("List(traceback keyword) = %+v, want no traceback search on lightweight list path", tracebackRows)
	}
	rows, err := s.List(context.Background(), ListFilter{Category: "rpc", Severity: "error", Keyword: "failed", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	row := assertBusLogListProjection(t, rows)
	detail, err := s.Get(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertBusLogDetailProjection(t, detail)
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

func assertBusLogListProjection(t *testing.T, rows []BusExceptionLog) BusExceptionLog {
	t.Helper()
	if len(rows) != 1 {
		t.Fatalf("List() = %+v, want one row", rows)
	}
	row := rows[0]
	if row.ID == 0 {
		t.Fatalf("List() row ID = 0: %+v", row)
	}
	if row.Category != "rpc" {
		t.Fatalf("List() row category = %q, want rpc: %+v", row.Category, row)
	}
	if row.Traceback != "" {
		t.Fatalf("List() row traceback = %q, want lightweight empty traceback", row.Traceback)
	}
	if string(row.Extra) != `{}` {
		t.Fatalf("List() row extra = %s, want lightweight empty object", row.Extra)
	}
	if !row.HasTraceback || !row.HasExtra {
		t.Fatalf("List() row flags = (%v, %v), want heavy-field flags", row.HasTraceback, row.HasExtra)
	}
	return row
}

func assertBusLogDetailProjection(t *testing.T, detail BusExceptionLog) {
	t.Helper()
	if detail.Traceback != "STACK payload" {
		t.Fatalf("Get() detail traceback = %q, want full traceback", detail.Traceback)
	}
	if string(detail.Extra) != `{"large":"bus"}` {
		t.Fatalf("Get() detail extra = %s, want full extra payload", detail.Extra)
	}
}
