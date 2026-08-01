package gate

import (
	"strings"
	"testing"
)

func TestDurationLedgerSQLiteV1ReconcilesCompatiblePassIndex(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`DROP INDEX idx_ci_workload_pass_compatible`); err != nil {
		t.Fatal(err)
	}
	var schemaVersion int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&schemaVersion); err != nil {
		t.Fatal(err)
	}
	if schemaVersion != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("schema version = %d", schemaVersion)
	}
	if err := ensureDurationLedgerSQLiteSchema(database); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = 'idx_ci_workload_pass_compatible'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("compatible pass index count = %d", count)
	}
	assertSQLiteQueryPlanUsesIndex(t, database, `
		SELECT identity_digest FROM ci_workload_pass_proofs
		WHERE workload_id = ? AND execution_digest = ? AND environment_digest = ?
		ORDER BY observed_at_unix_ms DESC
	`, "idx_ci_workload_pass_compatible", "unit", strings.Repeat("a", 64), "sha256:environment")
}
