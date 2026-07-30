package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const (
	legacy120AppliedAt = 4800123
	targetAppliedAt    = 4800456
)

type legacy120FixtureKind string

const (
	managedGenerationFixture legacy120FixtureKind = "managed-generation"
	providerRecoveryFixture  legacy120FixtureKind = "provider-recovery"
)

type legacy120Fixture struct {
	ctx            context.Context
	db             *sql.DB
	kind           legacy120FixtureKind
	legacyFilename string
	targetFilename string
	targetVersion  int
}

func setupLegacy120CompatibilityFixture(
	t *testing.T,
	kind legacy120FixtureKind,
	targetExists bool,
) legacy120Fixture {
	t.Helper()
	ctx, db := openBranchLocalPreUpgradeDB(t)
	fixture := legacy120Fixture{ctx: ctx, db: db, kind: kind}
	switch kind {
	case managedGenerationFixture:
		fixture.legacyFilename = legacyManagedGeneration120
		fixture.targetFilename = canonicalManagedGeneration122
		fixture.targetVersion = 122
		setupLegacyManagedGenerationState(t, db)
	case providerRecoveryFixture:
		fixture.legacyFilename = legacyProviderRecovery120
		fixture.targetFilename = canonicalProviderRecovery123
		fixture.targetVersion = 123
		setupLegacyProviderRecoveryState(t, ctx, db)
	default:
		t.Fatalf("unknown legacy 120 fixture kind %q", kind)
	}
	insertMigrationMarker(t, db, 120, fixture.legacyFilename, legacy120AppliedAt)
	if targetExists {
		insertMigrationMarker(t, db, fixture.targetVersion, fixture.targetFilename, targetAppliedAt)
	}
	return fixture
}

func setupLegacyManagedGenerationState(t *testing.T, db *sql.DB) {
	t.Helper()
	mustExec(t, db, readMigrationTestFile(t, canonicalManagedGeneration122))
	mustExec(t, db, `
		INSERT INTO mcp_managed_generation_instances(instance_id) VALUES ('compat-instance');
		INSERT INTO mcp_managed_generations(instance_id, generation, claim_id, external_committed)
		VALUES ('compat-instance', 9, 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb', 1)
	`)
}

func setupLegacyProviderRecoveryState(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	mustExec(t, db, `
		INSERT INTO agent_provider_binding (
			agent_id, provider, provider_thread_id, session_uuid, codex_home
		) VALUES (
			'compat-agent', 'codex',
			'019E218FB5147733BE85B3EE7F6A78B1',
			'019E218F-B514-7733-BE85-B3EE7F6A78B2',
			'/instances/compat-codex'
		)
	`)
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin legacy provider recovery fixture: %v", err)
	}
	if err := migrateAgentProviderBindingRecoveryOwner(ctx, tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("apply legacy provider recovery state: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit legacy provider recovery state: %v", err)
	}
}

func insertMigrationMarker(t *testing.T, db *sql.DB, version int, filename string, appliedAt int64) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO schema_migrations(version, name, filename, applied_at)
		VALUES (?, ?, ?, ?)
	`, version, strings.TrimSuffix(filename, ".sql"), filename, appliedAt); err != nil {
		t.Fatalf("insert migration marker %s: %v", filename, err)
	}
}

func legacy120CompatibilityDir(t *testing.T, terminalBody string, targetFilename string) string {
	t.Helper()
	dir := t.TempDir()
	writeMigrationTestFile(t, dir, terminalOutcomeOutboxMigration, terminalBody)
	writeMigrationTestFile(t, dir, targetFilename, readMigrationTestFile(t, targetFilename))
	return dir
}

func copyMigrationsThroughVersion(t *testing.T, sourceDir string, maxVersion int) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatalf("read migration source %s: %v", sourceDir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".sql") {
			continue
		}
		version := parseMigrationVersion(name)
		if version <= 0 || version > maxVersion {
			continue
		}
		body, err := os.ReadFile(filepath.Join(sourceDir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		writeMigrationTestFile(t, dir, name, string(body))
	}
	return dir
}

func assertLegacy120CompatibilityApplied(t *testing.T, fixture legacy120Fixture, targetWasPresent bool) {
	t.Helper()
	assertMigrationMarkerCount(t, fixture.db, fixture.legacyFilename, 0)
	assertMigrationMarkerCount(t, fixture.db, terminalOutcomeOutboxMigration, 1)
	assertMigrationMarkerCount(t, fixture.db, fixture.targetFilename, 1)
	wantAppliedAt := int64(legacy120AppliedAt)
	if targetWasPresent {
		wantAppliedAt = targetAppliedAt
	}
	assertMigrationAppliedAt(t, fixture.db, fixture.targetFilename, wantAppliedAt)
	assertLegacy120Payload(t, fixture)
}

func assertMigrationAppliedAt(t *testing.T, db *sql.DB, filename string, want int64) {
	t.Helper()
	var got int64
	if err := db.QueryRow(
		"SELECT applied_at FROM schema_migrations WHERE filename = ?",
		filename,
	).Scan(&got); err != nil {
		t.Fatalf("read migration applied_at for %s: %v", filename, err)
	}
	if got != want {
		t.Fatalf("migration %s applied_at = %d, want %d", filename, got, want)
	}
}

func assertLegacy120Payload(t *testing.T, fixture legacy120Fixture) {
	t.Helper()
	switch fixture.kind {
	case managedGenerationFixture:
		assertManagedGenerationRow(t, fixture.db, "compat-instance", 9, 1)
		assertManagedGenerationOwnerID(t, fixture.db, 1)
	case providerRecoveryFixture:
		var providerThreadID, sessionUUID, recoveryHome string
		if err := fixture.db.QueryRow(`
			SELECT provider_thread_id, session_uuid, provider_recovery_home
			FROM agent_provider_binding WHERE agent_id = 'compat-agent'
		`).Scan(&providerThreadID, &sessionUUID, &recoveryHome); err != nil {
			t.Fatalf("read provider recovery compatibility row: %v", err)
		}
		if providerThreadID != "019e218f-b514-7733-be85-b3ee7f6a78b1" ||
			sessionUUID != "019e218f-b514-7733-be85-b3ee7f6a78b2" ||
			recoveryHome != "/instances/compat-codex" {
			t.Fatalf("provider recovery compatibility row = %q/%q/%q", providerThreadID, sessionUUID, recoveryHome)
		}
	default:
		t.Fatalf("unknown legacy 120 payload kind %q", fixture.kind)
	}
}

func legacy120DatabaseSnapshot(t *testing.T, db *sql.DB) []string {
	t.Helper()
	var snapshot []string
	appendSQLiteMasterSnapshot(t, db, &snapshot)
	appendMigrationMarkerRowsSnapshot(t, db, &snapshot)
	appendOptionalManagedGenerationSnapshot(t, db, &snapshot)
	appendOptionalProviderRecoverySnapshot(t, db, &snapshot)
	return snapshot
}

func appendSQLiteMasterSnapshot(t *testing.T, db *sql.DB, snapshot *[]string) {
	t.Helper()
	rows, err := db.Query(`
		SELECT type, name, tbl_name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_%'
		ORDER BY type, name
	`)
	if err != nil {
		t.Fatalf("read sqlite_master snapshot: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var objectType, name, table, sqlText string
		if err := rows.Scan(&objectType, &name, &table, &sqlText); err != nil {
			t.Fatalf("scan sqlite_master snapshot: %v", err)
		}
		*snapshot = append(*snapshot, fmt.Sprintf("schema|%s|%s|%s|%s", objectType, name, table, sqlText))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate sqlite_master snapshot: %v", err)
	}
}

func appendOptionalManagedGenerationSnapshot(t *testing.T, db *sql.DB, snapshot *[]string) {
	t.Helper()
	tables := sqliteTables(t, db)
	if tables["mcp_managed_generation_owner"] {
		appendQuerySnapshot(t, db, snapshot, "managed-owner", `
			SELECT singleton_id, owner_epoch, marker_initialized, ledger_initialized
			FROM mcp_managed_generation_owner ORDER BY singleton_id
		`)
	}
	if tables["mcp_managed_generation_instances"] {
		appendQuerySnapshot(t, db, snapshot, "managed-instance", `
			SELECT instance_id FROM mcp_managed_generation_instances ORDER BY instance_id
		`)
	}
	if tables["mcp_managed_generations"] {
		appendQuerySnapshot(t, db, snapshot, "managed-generation", `
			SELECT instance_id, generation, claim_id, external_committed
			FROM mcp_managed_generations ORDER BY instance_id
		`)
	}
}

func appendOptionalProviderRecoverySnapshot(t *testing.T, db *sql.DB, snapshot *[]string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM pragma_table_info('agent_provider_binding')
		WHERE name = 'provider_recovery_home'
	`).Scan(&count); err != nil {
		t.Fatalf("inspect provider recovery snapshot columns: %v", err)
	}
	if count != 1 {
		return
	}
	appendQuerySnapshot(t, db, snapshot, "provider-binding", `
		SELECT agent_id, provider, provider_thread_id, session_uuid,
		       codex_home, codex_instance_key, codex_model_provider, provider_recovery_home
		FROM agent_provider_binding ORDER BY agent_id
	`)
}

func appendQuerySnapshot(t *testing.T, db *sql.DB, snapshot *[]string, label, query string) {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("read %s snapshot: %v", label, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatalf("read %s snapshot columns: %v", label, err)
	}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatalf("scan %s snapshot: %v", label, err)
		}
		*snapshot = append(*snapshot, label+"|"+fmt.Sprint(values))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate %s snapshot: %v", label, err)
	}
}

func assertLegacy120RollbackSnapshot(t *testing.T, db *sql.DB, before []string) {
	t.Helper()
	after := legacy120DatabaseSnapshot(t, db)
	if !slices.Equal(before, after) {
		t.Fatalf("legacy 120 rollback snapshot changed\nbefore=%q\nafter=%q", before, after)
	}
}
