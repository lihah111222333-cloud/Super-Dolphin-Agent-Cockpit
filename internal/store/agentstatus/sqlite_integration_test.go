package agentstatus

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestSQLiteAgentStatusUpsertGetAndList(t *testing.T) {
	t.Parallel()

	db := openAgentStatusSQLiteDB(t)
	s := NewStore(sqlc.New(db))

	created, err := s.Upsert(context.Background(), UpsertParams{
		AgentID:     "agent-1",
		AgentName:   "Agent One",
		SessionID:   "session-1",
		Status:      "running",
		StagnantSec: 7,
		OutputTail:  json.RawMessage(`["ready"]`),
	})
	if err != nil {
		t.Fatalf("Upsert() error = %v", err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatalf("Upsert() timestamps were not populated: %+v", created)
	}

	assertAgentStatusGet(t, s)
	assertAgentStatusList(t, s)
	assertAgentStatusRejectsInvalidJSON(t, s)
}

func assertAgentStatusGet(t *testing.T, s Store) {
	t.Helper()

	got, err := s.Get(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.AgentID != "agent-1" || string(got.OutputTail) != `["ready"]` {
		t.Fatalf("Get() = %+v", got)
	}
}

func assertAgentStatusList(t *testing.T, s Store) {
	t.Helper()

	rows, err := s.List(context.Background(), "running")
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(rows) != 1 || rows[0].AgentID != "agent-1" {
		t.Fatalf("List() = %+v", rows)
	}
}

func assertAgentStatusRejectsInvalidJSON(t *testing.T, s Store) {
	t.Helper()

	if _, err := s.Upsert(context.Background(), UpsertParams{AgentID: "bad-json", Status: "running", OutputTail: json.RawMessage(`not-json`)}); err == nil {
		t.Fatal("Upsert() invalid JSON error = nil")
	}
}

func openAgentStatusSQLiteDB(t *testing.T) *sql.DB {
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
