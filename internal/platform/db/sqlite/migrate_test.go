package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func TestRunMigrationsRollsBackBodyAndMarkerOnFailure(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "001_baseline.sql", baselineMigrationForTest())
	writeMigrationTestFile(t, dir, "002_bad.sql", `
CREATE TABLE rollback_probe(id INTEGER PRIMARY KEY);
INSERT INTO missing_table(id) VALUES (1);
`)

	err := RunMigrations(ctx, db, dir)
	if err == nil {
		t.Fatal("RunMigrations() error = nil, want migration failure")
	}
	assertMigrationTableMissing(t, db, "rollback_probe")
	assertMigrationMarkerCount(t, db, "001_baseline.sql", 1)
	assertMigrationMarkerCount(t, db, "002_bad.sql", 0)
}

func TestRunMigrationsRejectsInvalidVersionWithoutMarker(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "001_baseline.sql", baselineMigrationForTest())
	writeMigrationTestFile(t, dir, "bad_name.sql", "SELECT 1;\n")

	err := RunMigrations(ctx, db, dir)
	if err == nil {
		t.Fatal("RunMigrations() error = nil, want invalid version failure")
	}
	assertMigrationMarkerCount(t, db, "bad_name.sql", 0)
}

func TestRunMigrationsAcceptsSelfRecordedBaselineMarker(t *testing.T) {
	ctx := context.Background()
	db := openMigrationTestDB(t)
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, "001_baseline.sql", baselineMigrationForTest())

	if err := RunMigrations(ctx, db, dir); err != nil {
		t.Fatalf("RunMigrations() error = %v", err)
	}
	assertMigrationMarkerCount(t, db, "001_baseline.sql", 1)
	var version int
	if err := db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("query max migration version: %v", err)
	}
	if version != 103 {
		t.Fatalf("max migration version = %d, want 103", version)
	}
}

func baselineMigrationForTest() string {
	return `
CREATE TABLE schema_migrations (
	version INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	filename TEXT NOT NULL,
	applied_at INTEGER NOT NULL
);
CREATE TABLE runtime_probe(id INTEGER PRIMARY KEY);
INSERT OR IGNORE INTO schema_migrations(version, name, filename, applied_at)
VALUES (103, 'sqlite baseline', '001_baseline.sql', 0);
`
}

func openMigrationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open(driverName, filepath.Join(t.TempDir(), "migration-test.db"))
	if err != nil {
		t.Fatalf("open migration test DB: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func writeMigrationTestFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
		t.Fatalf("write migration %s: %v", name, err)
	}
}

func assertMigrationTableMissing(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var name string
	err := db.QueryRow("SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
	if err == sql.ErrNoRows {
		return
	}
	if err != nil {
		t.Fatalf("probe table %s: %v", table, err)
	}
	t.Fatalf("table %s exists after failed migration", table)
}

func assertMigrationMarkerCount(t *testing.T, db *sql.DB, filename string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow("SELECT COUNT(*) FROM schema_migrations WHERE filename = ?", filename).Scan(&got); err != nil {
		t.Fatalf("count migration marker %s: %v", filename, err)
	}
	if got != want {
		t.Fatalf("migration marker count for %s = %d, want %d", filename, got, want)
	}
}
