package sqlite

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func copyBranchLocalMigrationsBefore120(t *testing.T, sourceDir, targetDir string) {
	t.Helper()
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("read migration source: %v", err)
	}
	for _, entry := range entries {
		copyBranchLocalMigrationBefore120(t, sourceDir, targetDir, entry)
	}
}

func copyBranchLocalMigrationBefore120(
	t *testing.T,
	sourceDir string,
	targetDir string,
	entry os.DirEntry,
) {
	t.Helper()
	name := entry.Name()
	if entry.IsDir() || !strings.HasSuffix(name, ".sql") || name >= "120_" {
		return
	}
	body, err := os.ReadFile(filepath.Join(sourceDir, name))
	if err != nil {
		t.Fatalf("read migration %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(targetDir, name), body, 0o600); err != nil {
		t.Fatalf("copy migration %s: %v", name, err)
	}
}

func TestCopyBranchLocalMigrationsBefore120KeepsBoundaryAt119(t *testing.T) {
	sourceDir := t.TempDir()
	targetDir := t.TempDir()
	for _, name := range []string{
		"119_before_r1.sql",
		"120_t1.sql",
		"121_t1.sql",
		"122_a1.sql",
		"123_r1.sql",
	} {
		writeMigrationTestFile(t, sourceDir, name, "SELECT 1;\n")
	}

	copyBranchLocalMigrationsBefore120(t, sourceDir, targetDir)
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		t.Fatalf("read branch-local migration fixture: %v", err)
	}
	names := migrationEntryNames(entries)
	if !slices.Equal(names, []string{"119_before_r1.sql"}) {
		t.Fatalf("branch-local migrations = %v, want only pre-R1 version 119", names)
	}
}

func migrationEntryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
