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

	upsertUIPreference(t, s, "global", UpsertParams{Cwd: "", Key: "theme", Value: json.RawMessage(`"dark"`)})
	upsertUIPreference(t, s, "project", UpsertParams{Cwd: "/proj", Key: "layout", Value: json.RawMessage(`{"mode":"wide"}`)})
	upsertUIPreference(t, s, "other", UpsertParams{Cwd: "/other", Key: "layout", Value: json.RawMessage(`"narrow"`)})

	assertUIPreferenceValue(t, s)
	assertUIPreferenceList(t, s)
	assertUIPreferenceRejectsInvalidJSON(t, s)
}

func upsertUIPreference(t *testing.T, s Store, label string, params UpsertParams) {
	t.Helper()

	if err := s.Upsert(context.Background(), params); err != nil {
		t.Fatalf("Upsert(%s) error = %v", label, err)
	}
}

func assertUIPreferenceValue(t *testing.T, s Store) {
	t.Helper()

	value, err := s.GetValue(context.Background(), "/proj", "layout")
	if err != nil {
		t.Fatalf("GetValue() error = %v", err)
	}
	if string(value) != `{"mode":"wide"}` {
		t.Fatalf("GetValue() = %s", value)
	}
}

func assertUIPreferenceList(t *testing.T, s Store) {
	t.Helper()

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
}

func assertUIPreferenceRejectsInvalidJSON(t *testing.T, s Store) {
	t.Helper()

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
