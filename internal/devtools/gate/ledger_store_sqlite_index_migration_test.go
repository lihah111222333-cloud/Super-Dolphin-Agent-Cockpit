package gate

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestAcceptedGenerationMigrationRejectsMixedNonemptyRootsWithoutMutation(t *testing.T) {
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(t.TempDir()+"/mixed-retention.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE duration_samples (id INTEGER PRIMARY KEY);
		CREATE TABLE ci_catalog_observations (accepted_generation TEXT NOT NULL);
		CREATE TABLE ci_runs (job_id TEXT PRIMARY KEY, accepted_generation TEXT NOT NULL);
		CREATE TABLE remote_ci_calibration_checkpoints (identity TEXT PRIMARY KEY, accepted_generation TEXT NOT NULL);
		INSERT INTO ci_runs (job_id, accepted_generation) VALUES ('preserve-me', '7');
	`); err != nil {
		t.Fatal(err)
	}
	if err := ensureAcceptedGenerationRetentionSchema(database); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("mixed nonempty retention migration error = %v, want ErrMigrationRequired", err)
	}
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_runs WHERE job_id = 'preserve-me'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("canonical run rows after rejected migration = %d, want 1", rows)
	}
	columns := durationLedgerSQLiteTableColumnsForTest(t, database, "duration_samples")
	if columns[cicontract.AcceptedGenerationColumn] {
		t.Fatal("rejected mixed migration partially mutated the legacy root")
	}
}

func TestDurationLedgerSQLiteMigratesLegacyMetadataToSQLAuthority(t *testing.T) {
	path := t.TempDir() + "/duration-ledger.sqlite"
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`
		CREATE TABLE duration_ledger_meta (
			singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
			schema_version INTEGER NOT NULL CHECK (schema_version = 1),
			generation TEXT NOT NULL,
			ledger_version INTEGER NOT NULL,
			legacy_source_sha256 TEXT NOT NULL DEFAULT ''
		);
		INSERT INTO duration_ledger_meta (singleton, schema_version, generation, ledger_version)
		VALUES (1, 1, '7', 1);
		PRAGMA user_version = 1;
	`); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatal(err)
	}
	columns := durationLedgerSQLiteTableColumnsForTest(t, database, "duration_ledger_meta")
	if columns["legacy_source_sha256"] || !columns["authority_id"] {
		t.Fatalf("duration ledger metadata columns = %v", columns)
	}
	var authorityID string
	if err := database.QueryRow(`SELECT authority_id FROM duration_ledger_meta WHERE singleton = 1`).Scan(&authorityID); err != nil {
		t.Fatal(err)
	}
	if authorityID != cicontract.SQLAuthorityID {
		t.Fatalf("authority ID = %q, want %q", authorityID, cicontract.SQLAuthorityID)
	}
}

func TestDurationLedgerSQLiteRejectsUnknownAuthorityMetadata(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`UPDATE duration_ledger_meta SET authority_id = 'legacy-duration-ledger-json/v1'`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "authority ID") {
		t.Fatalf("load unknown authority metadata error = %v", err)
	}
	if _, err := store.CompareAndSwap(1, NewDurationLedger()); err == nil || !strings.Contains(err.Error(), "authority ID") {
		t.Fatalf("CAS unknown authority metadata error = %v", err)
	}
}

func TestDurationLedgerSQLiteAuthorityBindingGuardFailsForMissingCanonicalTable(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`DROP TABLE ci_timing_observations`); err != nil {
		t.Fatal(err)
	}
	if err := verifyDurationLedgerSQLAuthorityBindings(database); err == nil ||
		!strings.Contains(err.Error(), "ci_timing_observations") {
		t.Fatalf("missing canonical table error = %v", err)
	}
}

func TestDurationLedgerSQLiteRejectsRetiredRunPhaseTimingTable(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`CREATE TABLE ci_run_phase_timings (job_id TEXT NOT NULL)`); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "retired remote CI phase timing table") {
		t.Fatalf("retired timing source error = %v", err)
	}
}

func durationLedgerSQLiteTableColumnsForTest(t *testing.T, database *sql.DB, table string) map[string]bool {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	columns, err := durationLedgerSQLiteTableColumns(transaction, table)
	if err != nil {
		t.Fatal(err)
	}
	return columns
}
