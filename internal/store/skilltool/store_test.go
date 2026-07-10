package skilltool

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

func TestNewRejectsMissingDatabase(t *testing.T) {
	t.Parallel()

	if got := New(nil); got != nil {
		t.Fatalf("New(nil) = %#v, want nil", got)
	}
	var store *Store
	_, err := store.List(context.Background(), ListQuery{CWD: "/repo", Limit: 20})
	if !errors.Is(err, ErrStoreNotConfigured) {
		t.Fatalf("nil Store.List error = %v, want ErrStoreNotConfigured", err)
	}
}

func TestListCreatesSkillToolsTableLazily(t *testing.T) {
	t.Parallel()

	db := openSQLite(t)
	store := New(db)
	if tableExists(t, db) {
		t.Fatal("skill_tools exists before first persistence call")
	}
	rows, err := store.List(context.Background(), ListQuery{CWD: "/repo", Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if rows == nil || len(rows) != 0 {
		t.Fatalf("List rows = %#v, want non-nil empty", rows)
	}
	if !tableExists(t, db) {
		t.Fatal("skill_tools was not created lazily")
	}
}

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close SQLite: %v", err)
		}
	})
	return db
}

func tableExists(t *testing.T, db *sql.DB) bool {
	t.Helper()
	var name string
	err := db.QueryRow(`SELECT name FROM sqlite_master WHERE type='table' AND name='skill_tools'`).Scan(&name)
	if err == nil {
		return name == "skill_tools"
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	t.Fatalf("query sqlite_master: %v", err)
	return false
}
