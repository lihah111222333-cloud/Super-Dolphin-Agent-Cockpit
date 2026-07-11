package routingtest

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/sqlc"
	_ "modernc.org/sqlite"
)

func TestSQLiteRoutingTestListEnabled(t *testing.T) {
	t.Parallel()

	db := openRoutingTestSQLiteDB(t)
	insertRoutingTestFixture(t, db, "input enabled", "main/general", 1, 2_000)
	insertRoutingTestFixture(t, db, "input disabled", "main/other", 0, 1_000)

	rows, err := NewStore(sqlc.New(db)).ListEnabled(context.Background())
	if err != nil {
		t.Fatalf("ListEnabled() error = %v", err)
	}
	if len(rows) != 1 || rows[0].Input != "input enabled" || !rows[0].Enabled {
		t.Fatalf("ListEnabled() = %+v", rows)
	}
	if rows[0].CreatedAt.IsZero() || rows[0].UpdatedAt.IsZero() {
		t.Fatalf("ListEnabled() timestamps were not mapped: %+v", rows[0])
	}
}

func openRoutingTestSQLiteDB(t *testing.T) *sql.DB {
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

func insertRoutingTestFixture(t *testing.T, db *sql.DB, input, expected string, enabled int, updatedAt int64) {
	t.Helper()

	_, err := db.Exec(`
INSERT INTO prompt_routing_tests (input, expected_prompt_key, note, enabled, created_at, updated_at)
VALUES (?, ?, '', ?, ?, ?);
`, input, expected, enabled, updatedAt, updatedAt)
	if err != nil {
		t.Fatalf("insert routing test fixture: %v", err)
	}
}
