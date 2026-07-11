package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	sqliteruntime "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/db/sqlite"
)

func runFixtureMigrations(t *testing.T, database *sql.DB) error {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return sqliteruntime.RunMigrations(context.Background(), database, sqliteMigrationsDir(root))
}

func assertIntPragma(t *testing.T, database *sql.DB, name string, want int) {
	t.Helper()
	var got int
	if err := database.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s scan error = %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %d, want %d", name, got, want)
	}
}

func assertTextPragma(t *testing.T, database *sql.DB, name, want string) {
	t.Helper()
	var got string
	if err := database.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s scan error = %v", name, err)
	}
	if got != want {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}
