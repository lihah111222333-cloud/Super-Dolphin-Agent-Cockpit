package auditlog

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

func TestSQLiteAuditLogInsertAndListFilters(t *testing.T) {
	t.Parallel()

	db := openAuditLogSQLiteDB(t)
	s := NewStore(sqlc.New(db))

	if err := s.Insert(context.Background(), InsertParams{
		EventType: "tool",
		Action:    "Create",
		Result:    "ok",
		Actor:     "alice",
		Target:    "target-1",
		Detail:    "created target",
		Level:     "info",
		Extra:     json.RawMessage(`{"large":"audit"}`),
	}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	if err := s.Insert(context.Background(), InsertParams{EventType: "tool", Extra: json.RawMessage(`not-json`)}); err == nil {
		t.Fatal("Insert() invalid JSON error = nil")
	}

	rows, err := s.List(context.Background(), ListFilter{EventType: "tool", Actor: "alice", Keyword: "create", Limit: 10})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 1 || rows[0].ID == 0 || rows[0].Action != "Create" {
		t.Fatalf("List() = %+v", rows)
	}
	if string(rows[0].Extra) != `{"large":"audit"}` {
		t.Fatalf("List() Extra = %s, want inserted extra roundtrip", rows[0].Extra)
	}
}

func openAuditLogSQLiteDB(t *testing.T) *sql.DB {
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
