package gate

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAcceptedBaselineStateProjectionRejectsCorruptionAndSnapshotDrift(t *testing.T) {
	stateJSON := `{"schema_version":13,"generation":7,"execution_provider":"aliyun-eci/v1","region_id":"cn-shenzhen","image_cache_snapshot_id":"snapshot-7"}`
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(stateJSON)))
	for name, test := range map[string]struct {
		state        string
		storedDigest string
		generation   uint64
	}{
		"corrupt JSON":        {state: `{`, storedDigest: digest, generation: 7},
		"forged digest":       {state: stateJSON, storedDigest: "sha256:forged", generation: 7},
		"generation mismatch": {state: stateJSON, storedDigest: digest, generation: 8},
		"legacy schema":       {state: `{"schema_version":12,"generation":7,"execution_provider":"aliyun-eci/v1","region_id":"cn-shenzhen","image_cache_snapshot_id":"snapshot-7"}`, generation: 7},
		"wrong provider":      {state: `{"schema_version":13,"generation":7,"execution_provider":"docker/v1","region_id":"cn-shenzhen","image_cache_snapshot_id":"snapshot-7"}`, generation: 7},
		"missing region":      {state: `{"schema_version":13,"generation":7,"execution_provider":"aliyun-eci/v1","image_cache_snapshot_id":"snapshot-7"}`, generation: 7},
		"missing snapshot":    {state: `{"schema_version":13,"generation":7,"execution_provider":"aliyun-eci/v1","region_id":"cn-shenzhen"}`, generation: 7},
	} {
		t.Run(name, func(t *testing.T) {
			storedDigest := test.storedDigest
			if storedDigest == "" {
				storedDigest = fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(test.state)))
			}
			if _, err := validateAcceptedBaselineStateProjection(test.state, storedDigest, test.generation); err == nil {
				t.Fatal("validateAcceptedBaselineStateProjection() unexpectedly accepted corrupted authority")
			}
		})
	}
	if snapshot, err := validateAcceptedBaselineStateProjection(stateJSON, digest, 7); err != nil || snapshot != "snapshot-7" {
		t.Fatalf("validateAcceptedBaselineStateProjection() = (%q, %v), want snapshot-7", snapshot, err)
	}
}

func TestDurationLedgerSQLiteRejectsLegacyMetadataWithoutAuthorityID(t *testing.T) {
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
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil {
		t.Fatalf("legacy metadata schema error = %v, want fail-fast refusal", err)
	}
	columns := sqliteTableColumns(t, database, "duration_ledger_meta")
	if !slices.Contains(columns, "legacy_source_sha256") || slices.Contains(columns, "authority_id") {
		t.Fatalf("rejected legacy metadata schema was mutated: columns = %v", columns)
	}
}

func TestDurationLedgerSQLiteRejectsLegacyAuthorityBeforeAnyWrite(t *testing.T) {
	for name, setup := range map[string]string{
		"retired workload table": `
			CREATE TABLE ci_workload_fingerprints (identity TEXT PRIMARY KEY);
			INSERT INTO ci_workload_fingerprints(identity) VALUES ('historical');
			PRAGMA user_version = 1;`,
		"retired workload table with zero version": `
			CREATE TABLE ci_workload_fingerprints (identity TEXT PRIMARY KEY);
			INSERT INTO ci_workload_fingerprints(identity) VALUES ('historical');`,
		"incomplete canonical table": `
			CREATE TABLE ci_runs (job_id TEXT PRIMARY KEY, source_tree_sha TEXT NOT NULL, status TEXT NOT NULL, catalog_digest TEXT NOT NULL);
			INSERT INTO ci_runs(job_id, source_tree_sha, status, catalog_digest) VALUES ('historical', 'tree', 'passed', 'catalog');
			PRAGMA user_version = 1;`,
	} {
		t.Run(name, func(t *testing.T) {
			database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(t.TempDir()+"/legacy-authority.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if _, err := database.Exec(setup); err != nil {
				t.Fatal(err)
			}
			beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
			beforeVersion := durationLedgerSQLiteUserVersionForTest(t, database)
			if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil {
				t.Fatalf("legacy authority schema error = %v, want fail-before-write refusal", err)
			}
			if afterMaster := durationLedgerSQLiteMasterForTest(t, database); afterMaster != beforeMaster {
				t.Fatalf("sqlite_master changed after rejected authority:\nwant %s\n got %s", beforeMaster, afterMaster)
			}
			if afterVersion := durationLedgerSQLiteUserVersionForTest(t, database); afterVersion != beforeVersion {
				t.Fatalf("user_version changed after rejected authority: got %d, want %d", afterVersion, beforeVersion)
			}
			var rows int
			if strings.Contains(name, "retired workload table") {
				err = database.QueryRow(`SELECT COUNT(*) FROM ci_workload_fingerprints WHERE identity = 'historical'`).Scan(&rows)
			} else {
				err = database.QueryRow(`SELECT COUNT(*) FROM ci_runs WHERE job_id = 'historical'`).Scan(&rows)
			}
			if err != nil || rows != 1 {
				t.Fatalf("legacy authority data changed: rows=%d err=%v", rows, err)
			}
		})
	}
}

func TestDurationLedgerSQLiteInitializesTrulyEmptyZeroVersionDatabase(t *testing.T) {
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(t.TempDir()+"/empty.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if version := durationLedgerSQLiteUserVersionForTest(t, database); version != 0 {
		t.Fatalf("initial user_version = %d, want 0", version)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatalf("initialize empty zero-version authority: %v", err)
	}
	if version := durationLedgerSQLiteUserVersionForTest(t, database); version != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("initialized user_version = %d, want %d", version, durationLedgerSQLiteSchemaVersion)
	}
	if err := verifyDurationLedgerSQLAuthorityBindings(database); err != nil {
		t.Fatalf("initialized authority bindings: %v", err)
	}
}

func TestDurationLedgerSQLitePreflightRejectsNonemptyMigrationRequiredShapesBeforeWrite(t *testing.T) {
	for name, test := range map[string]struct {
		setup, dataQuery string
	}{
		"baseline legacy JSON": {
			setup:     `CREATE TABLE ci_remote_baseline_state (singleton INTEGER PRIMARY KEY, legacy_json TEXT NOT NULL); INSERT INTO ci_remote_baseline_state VALUES (1, 'legacy'); PRAGMA user_version = 1;`,
			dataQuery: `SELECT legacy_json FROM ci_remote_baseline_state WHERE singleton = 1`,
		},
		"retention root missing generation": {
			setup:     `CREATE TABLE duration_samples (id INTEGER PRIMARY KEY, workload_id TEXT NOT NULL); INSERT INTO duration_samples VALUES (1, 'historical'); PRAGMA user_version = 1;`,
			dataQuery: `SELECT workload_id FROM duration_samples WHERE id = 1`,
		},
		"timing missing duration": {
			setup:     `CREATE TABLE ci_timing_observations (id INTEGER PRIMARY KEY, phase TEXT NOT NULL); INSERT INTO ci_timing_observations VALUES (1, 'test_body'); PRAGMA user_version = 1;`,
			dataQuery: `SELECT phase FROM ci_timing_observations WHERE id = 1`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(t.TempDir()+"/preflight.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			if _, err := database.Exec(test.setup); err != nil {
				t.Fatal(err)
			}
			beforeMaster := durationLedgerSQLiteMasterForTest(t, database)
			beforeVersion := durationLedgerSQLiteUserVersionForTest(t, database)
			var beforeData string
			if err := database.QueryRow(test.dataQuery).Scan(&beforeData); err != nil {
				t.Fatal(err)
			}
			if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil {
				t.Fatal("legacy migration-required shape was accepted")
			}
			if afterMaster := durationLedgerSQLiteMasterForTest(t, database); afterMaster != beforeMaster {
				t.Fatalf("sqlite_master changed after preflight refusal:\nwant %s\n got %s", beforeMaster, afterMaster)
			}
			if afterVersion := durationLedgerSQLiteUserVersionForTest(t, database); afterVersion != beforeVersion {
				t.Fatalf("user_version changed after preflight refusal: got %d, want %d", afterVersion, beforeVersion)
			}
			var afterData string
			if err := database.QueryRow(test.dataQuery).Scan(&afterData); err != nil || afterData != beforeData {
				t.Fatalf("legacy authority data changed: got %q err=%v, want %q", afterData, err, beforeData)
			}
		})
	}
}

func durationLedgerSQLiteMasterForTest(t *testing.T, database *sql.DB) string {
	t.Helper()
	rows, err := database.Query(`SELECT type, name, tbl_name, sql FROM sqlite_master ORDER BY type, name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries []string
	for rows.Next() {
		var (
			entry [3]string
			sql   sql.NullString
		)
		if err := rows.Scan(&entry[0], &entry[1], &entry[2], &sql); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, strings.Join(entry[:], "|")+"|"+sql.String)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(entries, "\n")
}

func durationLedgerSQLiteUserVersionForTest(t *testing.T, database *sql.DB) int {
	t.Helper()
	var version int
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	return version
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
	if _, err := store.Load(); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("retired timing source error = %v", err)
	}
}
