package gate

import (
	"database/sql"
	"reflect"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestDurationLedgerSQLiteMigratesV10CompileTimingSchema(t *testing.T) {
	database := openStrictSchemaTestDatabase(t, "compile-timing-v10.sqlite")
	defer database.Close()
	for _, statement := range durationLedgerSQLiteV10SchemaStatements() {
		if _, err := database.Exec(statement); err != nil {
			t.Fatalf("create v10 schema: %v", err)
		}
	}
	if _, err := database.Exec("PRAGMA user_version = 10"); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatalf("migrate v10 schema: %v", err)
	}
	if got := durationLedgerSQLiteUserVersionForTest(t, database); got != durationLedgerSQLiteSchemaVersion {
		t.Fatalf("migrated schema version = %d, want %d", got, durationLedgerSQLiteSchemaVersion)
	}
	if err := preflightDurationLedgerSQLiteExactSchema(database, durationLedgerSQLiteSchemaVersion); err != nil {
		t.Fatalf("migrated schema preflight: %v", err)
	}
	columns := sqliteTableColumns(t, database, cicontract.CompileTimingObservationsTable)
	for _, retiredColumn := range []string{"accepted_generation", "status", "authoritative", "cleanup_complete"} {
		if slices.Contains(columns, retiredColumn) {
			t.Fatalf("compile timing table repeats ci_runs authority column %q: %v", retiredColumn, columns)
		}
	}
}

func TestCompileTimingIdentityRejectsPaddedEnvironment(t *testing.T) {
	identity := compileTimingTestIdentity()
	identity.Platform = " " + identity.Platform
	if err := identity.Validate(); err == nil {
		t.Fatal("padded compile timing platform unexpectedly accepted")
	}
}

func TestCompileTimingObservationRejectsNonUTCTimestamps(t *testing.T) {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.FixedZone("local", 9*60*60))
	observation := compileTimingTestObservation(base, 100)
	if err := observation.Validate(); err == nil {
		t.Fatal("non-UTC compile timing observation unexpectedly accepted")
	}
}

func TestCompileTimingSQLiteRoundTripUsesRunContext(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	started := time.UnixMilli(1_700_000_000_000).UTC()
	jobID := "compile-roundtrip"
	insertCompileTimingTestRun(t, database, jobID, 7, "failed", false, true, started)
	want := CompileTimingObservation{
		Identity:    compileTimingTestIdentity(),
		DurationMS:  37,
		StartedAt:   started,
		CompletedAt: started.Add(37 * time.Millisecond),
		Measurement: cicontract.ObservationMeasured,
		Aggregation: cicontract.TimingAggregationRaw,
	}
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceSQLiteCompileTimingObservations(transaction, RemoteCIRunRecord{
		JobID:              jobID,
		AcceptedGeneration: 7,
		CompileTimingObservations: []CompileTimingObservation{
			want,
		},
	}); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := loadSQLiteCompileTimingObservations(database, jobID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []CompileTimingObservation{want}) {
		t.Fatalf("compile timing roundtrip = %#v, want %#v", got, []CompileTimingObservation{want})
	}
}

func TestCompileTimingSQLiteAuthorityWindowAndCascadeRetention(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	base := time.UnixMilli(1_700_100_000_000).UTC()
	seedCompileTimingAuthorityRows(t, database, base)
	index, err := loadSQLiteCompileTimingIndex(database)
	if err != nil {
		t.Fatal(err)
	}
	assertCompileTimingAuthorityWindow(t, index)
	assertCompileTimingCascade(t, database)
}

func seedCompileTimingAuthorityRows(t *testing.T, database *sql.DB, base time.Time) {
	t.Helper()
	for generation := uint64(1); generation <= 4; generation++ {
		jobID := "compile-authority-" + formatCompileTimingGeneration(generation)
		insertCompileTimingTestRun(t, database, jobID, generation, "passed", true, true, base.Add(time.Duration(generation)*time.Second))
		observation := compileTimingTestObservation(base.Add(time.Duration(generation)*time.Second), int64(generation*10))
		writeCompileTimingTestObservation(t, database, jobID, generation, observation)
	}
	insertCompileTimingTestRun(t, database, "compile-authority-empty", 5, "passed", true, true, base.Add(5*time.Second))
	insertCompileTimingTestRun(t, database, "compile-authority-failed", 5, "failed", false, true, base.Add(5*time.Second))
	writeCompileTimingTestObservation(t, database, "compile-authority-failed", 5, compileTimingTestObservation(base.Add(5*time.Second), 50))
}

func writeCompileTimingTestObservation(t *testing.T, database *sql.DB, jobID string, generation uint64, observation CompileTimingObservation) {
	t.Helper()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := replaceSQLiteCompileTimingObservations(transaction, RemoteCIRunRecord{JobID: jobID, AcceptedGeneration: generation, CompileTimingObservations: []CompileTimingObservation{observation}}); err != nil {
		_ = transaction.Rollback()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func assertCompileTimingAuthorityWindow(t *testing.T, index CompileTimingIndex) {
	t.Helper()
	if len(index.Samples) != 2 {
		t.Fatalf("authoritative compile timing samples = %d, want rows from latest three run generations", len(index.Samples))
	}
	gotGenerations := []uint64{index.Samples[0].AcceptedGeneration, index.Samples[1].AcceptedGeneration}
	if !slices.Equal(gotGenerations, []uint64{4, 3}) {
		t.Fatalf("compile timing generations = %v, want [4 3] after empty generation 5", gotGenerations)
	}
	if _, found, err := index.EstimateMS(compileTimingTestIdentity()); err != nil || !found {
		t.Fatalf("compile timing estimate found=%t err=%v, want authoritative hit", found, err)
	}
}

func assertCompileTimingCascade(t *testing.T, database *sql.DB) {
	t.Helper()
	var rows int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_compile_timing_observations WHERE job_id = ?`, "compile-authority-1").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("compile timing rows before cascade = %d, want 1", rows)
	}
	if _, err := database.Exec(`DELETE FROM ci_runs WHERE job_id = ?`, "compile-authority-1"); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_compile_timing_observations WHERE job_id = ?`, "compile-authority-1").Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Fatalf("compile timing rows after ci_runs cascade = %d, want 0", rows)
	}
}

func compileTimingTestIdentity() CompileTimingIdentity {
	return CompileTimingIdentity{
		PackageTarget:        "./internal/devtools/gate",
		SemanticKey:          CompileGroupSemanticGoTestNormal,
		Platform:             "linux/amd64",
		RunnerIdentityDigest: "runner-digest",
		ToolchainDigest:      "toolchain-digest",
		ExecutionMode:        DurationExecutionModeNormal,
		ResourceClassID:      "small",
		ResourceCPU:          2,
		ResourceMemoryGiB:    4,
	}
}

func compileTimingTestObservation(started time.Time, durationMS int64) CompileTimingObservation {
	return CompileTimingObservation{
		Identity:    compileTimingTestIdentity(),
		DurationMS:  durationMS,
		StartedAt:   started,
		CompletedAt: started.Add(time.Duration(durationMS) * time.Millisecond),
		Measurement: cicontract.ObservationMeasured,
		Aggregation: cicontract.TimingAggregationRaw,
	}
}

func insertCompileTimingTestRun(t *testing.T, database *sql.DB, jobID string, generation uint64, status string, authoritative, cleanupComplete bool, started time.Time) {
	t.Helper()
	if _, err := database.Exec(`
		INSERT INTO ci_runs (
			job_id, force, entrypoint, profile, plan_digest, catalog_digest,
			accepted_generation, image_cache_snapshot_id, source_tree_sha,
			candidate_gate_source_sha256, candidate_gate_toolchain_sha256,
			runner_image, status, authoritative, started_at_unix_ms,
			completed_at_unix_ms, cleanup_complete, error_text
		) VALUES (?, 0, 'manual_cli', 'local_fast', 'plan', 'catalog', ?,
			'snapshot', 'tree', '', '', 'runner-image', ?, ?, ?, ?, ?, '')
	`, jobID, strconv.FormatUint(generation, 10), status, boolToSQLite(authoritative), started.UnixMilli(), started.Add(time.Second).UnixMilli(), boolToSQLite(cleanupComplete)); err != nil {
		t.Fatalf("insert compile timing test run %q: %v", jobID, err)
	}
}

func formatCompileTimingGeneration(generation uint64) string {
	return strconv.FormatUint(generation, 10)
}
