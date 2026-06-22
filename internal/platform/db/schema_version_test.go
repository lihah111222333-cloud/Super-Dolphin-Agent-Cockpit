package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	if _, err := db.ExecContext(context.Background(),
		`CREATE TABLE schema_migrations (version INTEGER NOT NULL, name TEXT NOT NULL, filename TEXT NOT NULL, applied_at INTEGER NOT NULL)`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func insertVersion(t *testing.T, db *sql.DB, version int) {
	t.Helper()
	if _, err := db.ExecContext(context.Background(),
		`INSERT INTO schema_migrations(version,name,filename,applied_at) VALUES (?,?,?,?)`,
		version, "test", "test.sql", 0); err != nil {
		t.Fatal(err)
	}
}

func TestMinRequiredSchemaVersion(t *testing.T) {
	t.Parallel()
	if MinRequiredSchemaVersion != 107 {
		t.Fatalf("MinRequiredSchemaVersion = %d, want 107", MinRequiredSchemaVersion)
	}
}

func TestQuerySchemaVersion_AcceptsAtOrAboveMinimum(t *testing.T) {
	t.Parallel()
	for _, v := range []int{MinRequiredSchemaVersion, MinRequiredSchemaVersion + 1, MinRequiredSchemaVersion + 50} {
		v := v
		t.Run("", func(t *testing.T) {
			t.Parallel()
			db := openTestDB(t)
			insertVersion(t, db, v)
			var got int
			if err := querySchemaVersion(context.Background(), db, &got); err != nil {
				t.Fatalf("querySchemaVersion error = %v", err)
			}
			if got != v {
				t.Fatalf("got version %d, want %d", got, v)
			}
		})
	}
}

func TestVerifyMinSchemaVersion_RejectsBelowMinimum(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	insertVersion(t, db, MinRequiredSchemaVersion-1)
	var got int
	if err := querySchemaVersion(context.Background(), db, &got); err != nil {
		t.Fatalf("querySchemaVersion error = %v", err)
	}
	// Confirm the error message template is bilingual.
	msg := fmt.Sprintf(
		"数据库 migration 版本 < %d (当前=%d)，请先 apply 后再启动；database migration version below %d (current=%d), apply pending migrations before starting",
		MinRequiredSchemaVersion, got, MinRequiredSchemaVersion, got)
	for _, want := range []string{"migration", "apply", "数据库", "database migration version"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

func TestQuerySchemaVersion_PropagatesQueryError(t *testing.T) {
	t.Parallel()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got int
	if err := querySchemaVersion(context.Background(), db, &got); err == nil {
		t.Fatal("expected error from missing table, got nil")
	}
}
