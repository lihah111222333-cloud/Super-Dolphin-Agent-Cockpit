package uipreference

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

func TestSQLiteUIPreferenceUpsertGetAndList(t *testing.T) {
	t.Parallel()

	db := openUIPreferenceSQLiteDB(t)
	s := NewStore(sqlc.New(db))

	if err := s.Upsert(context.Background(), UpsertParams{Cwd: "", Key: "theme", Value: json.RawMessage(`"dark"`)}); err != nil {
		t.Fatalf("Upsert(global) error = %v", err)
	}
	if err := s.Upsert(context.Background(), UpsertParams{Cwd: "/proj", Key: "layout", Value: json.RawMessage(`{"mode":"wide"}`)}); err != nil {
		t.Fatalf("Upsert(project) error = %v", err)
	}
	if err := s.Upsert(context.Background(), UpsertParams{Cwd: "/other", Key: "layout", Value: json.RawMessage(`"narrow"`)}); err != nil {
		t.Fatalf("Upsert(other) error = %v", err)
	}

	value, err := s.GetValue(context.Background(), "/proj", "layout")
	if err != nil {
		t.Fatalf("GetValue() error = %v", err)
	}
	if string(value) != `{"mode":"wide"}` {
		t.Fatalf("GetValue() = %s", value)
	}

	rows, err := s.List(context.Background(), "/proj")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 2 || rows[0].Cwd != "" || rows[1].Cwd != "/proj" {
		t.Fatalf("List() = %+v", rows)
	}
	if rows[0].UpdatedAt.IsZero() || rows[1].UpdatedAt.IsZero() {
		t.Fatalf("List() timestamps were not populated: %+v", rows)
	}

	if err := s.Upsert(context.Background(), UpsertParams{Cwd: "/proj", Key: "bad", Value: json.RawMessage(`not-json`)}); err == nil {
		t.Fatal("Upsert() invalid JSON error = nil")
	}
}

func openUIPreferenceSQLiteDB(t *testing.T) *sql.DB {
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
