package sqlite

import (
	"context"
	"database/sql"
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

func TestRunMigrationsRealDBUpgradeBackfillsLegacyProviderThreadPlaceholder(t *testing.T) {
	ctx, db := openBranchLocalPreUpgradeDB(t)
	insertLegacyProviderThreadPlaceholders(t, db)

	if err := RunMigrations(ctx, db, "migrations"); err != nil {
		t.Fatalf("RunMigrations(123) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 123)
	assertProviderBindingIdentity(
		t,
		db,
		"agent-legacy-placeholder",
		"019e218f-b514-7733-be85-b3ee7f6a78a9",
	)
	assertProviderBindingIdentity(
		t,
		db,
		"agent-empty-provider-thread",
		"019e218f-b514-7733-be85-b3ee7f6a78ab",
	)
	assertProviderBindingIdentityTriggerRestored(t, db)
	assertMigrationMarkerCount(t, db, "123_agent_provider_binding_recovery_owner.sql", 1)
}

func openBranchLocalPreUpgradeDB(t *testing.T) (context.Context, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	db := openMigrationTestDB(t)
	preUpgradeDir := t.TempDir()
	copyBranchLocalMigrationsBefore120(t, "migrations", preUpgradeDir)
	if err := RunMigrations(ctx, db, preUpgradeDir); err != nil {
		t.Fatalf("RunMigrations(pre-123) error = %v", err)
	}
	assertMaxMigrationVersion(t, db, 119)
	return ctx, db
}

func insertLegacyProviderThreadPlaceholders(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		INSERT INTO agent_provider_binding (
			agent_id, provider, provider_thread_id, codex_thread_id, session_uuid
		) VALUES
			(
				'agent-legacy-placeholder', 'claude',
				'agent-legacy-placeholder', 'public-thread-placeholder',
				'019E218FB5147733BE85B3EE7F6A78A9'
			),
			(
				'agent-empty-provider-thread', 'claude',
				'', 'public-thread-empty',
				'019E218F-B514-7733-BE85-B3EE7F6A78AB'
			)
	`)
}

func assertProviderBindingIdentity(t *testing.T, db *sql.DB, agentID, expected string) {
	t.Helper()
	var providerThreadID, sessionUUID string
	if err := db.QueryRow(`
		SELECT provider_thread_id, session_uuid
		FROM agent_provider_binding
		WHERE agent_id = ?
	`, agentID).Scan(&providerThreadID, &sessionUUID); err != nil {
		t.Fatalf("read provider binding %s: %v", agentID, err)
	}
	if providerThreadID != expected || sessionUUID != expected {
		t.Fatalf(
			"provider binding %s = %q/%q, want %q/%q",
			agentID,
			providerThreadID,
			sessionUUID,
			expected,
			expected,
		)
	}
}

func assertProviderBindingIdentityTriggerRestored(t *testing.T, db *sql.DB) {
	t.Helper()
	_, err := db.Exec(`
		UPDATE agent_provider_binding
		SET provider_thread_id = '019e218f-b514-7733-be85-b3ee7f6a78aa'
		WHERE agent_id = 'agent-legacy-placeholder'
	`)
	if err == nil || !strings.Contains(err.Error(), "identity is immutable") {
		t.Fatalf("restored trigger update error = %v, want immutable rejection", err)
	}
}
