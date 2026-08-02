package gate

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestRemoteCIAuthorityRecordsRoundTripAndFailFast(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	const (
		jobID      = "refresh-job"
		generation = "7"
		treeSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	)
	prepareRemoteCIAuthorityRecordTest(t, store, jobID, generation, treeSHA)
	now := time.Date(2026, time.August, 3, 9, 0, 0, 0, time.UTC)
	delta := RemoteRefreshDeltaRecord{
		JobID: jobID, AttemptGeneration: 7, AcceptedGeneration: 6,
		AcceptedStateSHA256: "sha256:" + strings.Repeat("1", 64), AcceptedSnapshotID: "snapshot-6",
		DeltaIdentity: "delta-7", DeltaSHA256: "sha256:" + strings.Repeat("2", 64), DeltaSizeBytes: 42,
		TargetTreeSHA: treeSHA, TargetClosureSHA256: "sha256:" + strings.Repeat("3", 64),
		TransferMode: cicontract.RefreshTransferAcceptedSnapshotDelta, RecordedAt: now,
	}
	if err := store.AppendRemoteRefreshDelta(delta); err != nil {
		t.Fatalf("AppendRemoteRefreshDelta() error = %v", err)
	}
	if err := store.AppendRemoteRefreshDelta(delta); err != nil {
		t.Fatalf("exact duplicate refresh delta error = %v", err)
	}
	conflictingDelta := delta
	conflictingDelta.DeltaSHA256 = "sha256:" + strings.Repeat("4", 64)
	if err := store.AppendRemoteRefreshDelta(conflictingDelta); err == nil || !strings.Contains(err.Error(), "duplicate conflicts") {
		t.Fatalf("conflicting duplicate refresh delta error = %v", err)
	}
	wrongGeneration := delta
	wrongGeneration.AcceptedGeneration = 5
	wrongGeneration.DeltaIdentity = "delta-wrong-generation"
	wrongGeneration.DeltaSHA256 = "sha256:" + strings.Repeat("9", 64)
	if err := store.AppendRemoteRefreshDelta(wrongGeneration); err == nil || !strings.Contains(err.Error(), "authority binding") {
		t.Fatalf("wrong accepted generation error = %v", err)
	}
	deltas, err := store.LoadRemoteRefreshDeltas(jobID, 7)
	if err != nil {
		t.Fatalf("LoadRemoteRefreshDeltas() error = %v", err)
	}
	if !reflect.DeepEqual(deltas, []RemoteRefreshDeltaRecord{delta}) {
		t.Fatalf("refresh deltas = %#v, want %#v", deltas, []RemoteRefreshDeltaRecord{delta})
	}

	receipts := testCompleteCheckReceipts(jobID, treeSHA, now)
	if err := store.AppendCheckReceipts(receipts); err != nil {
		t.Fatalf("AppendCheckReceipts() error = %v", err)
	}
	if err := store.AppendCheckReceipts(receipts); err == nil {
		t.Fatal("duplicate check receipts were accepted")
	}
	loaded, err := store.LoadCheckReceipts(jobID)
	if err != nil {
		t.Fatalf("LoadCheckReceipts() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, receipts) {
		t.Fatalf("check receipts = %#v, want %#v", loaded, receipts)
	}

	failed := append([]CheckReceiptRecord(nil), receipts...)
	failed[0].Passed = false
	if err := validateCompletePassingCheckReceipts(failed); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("failed check validation error = %v", err)
	}
	zeroGeneration := receipts[0]
	zeroGeneration.AcceptedGeneration = 0
	if err := validateCheckReceiptRecord(zeroGeneration); err == nil || !strings.Contains(err.Error(), "generation") {
		t.Fatalf("zero check receipt generation error = %v", err)
	}
	maxGenerationReceipt := receipts[0]
	maxGenerationReceipt.AcceptedGeneration = ^uint64(0)
	maxGenerationReceipt.ReceiptSHA256, err = CheckReceiptSHA256(maxGenerationReceipt)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCheckReceiptRecord(maxGenerationReceipt); err != nil {
		t.Fatalf("maximum uint64 check receipt generation error = %v", err)
	}
	tampered := receipts[0]
	tampered.CompletedAt = tampered.CompletedAt.Add(time.Second)
	tampered.Duration = tampered.CompletedAt.Sub(tampered.StartedAt)
	if err := validateCheckReceiptRecord(tampered); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered check receipt error = %v", err)
	}
	zeroDeltaGeneration := delta
	zeroDeltaGeneration.AttemptGeneration = 0
	if err := validateRemoteRefreshDeltaRecord(zeroDeltaGeneration); err == nil || !strings.Contains(err.Error(), "generations") {
		t.Fatalf("zero refresh delta generation error = %v", err)
	}
	maxGenerationDelta := delta
	maxGenerationDelta.AcceptedGeneration = ^uint64(0)
	if err := validateRemoteRefreshDeltaRecord(maxGenerationDelta); err != nil {
		t.Fatalf("maximum uint64 refresh delta generation error = %v", err)
	}
}

func TestLoadCheckReceiptsRejectsStoredFailureAndSchemaReaddsMissingTables(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	const jobID = "receipt-job"
	treeSHA := strings.Repeat("b", 40)
	prepareRemoteCIAuthorityRecordTest(t, store, jobID, "9", treeSHA)
	now := time.Date(2026, time.August, 3, 10, 0, 0, 0, time.UTC)
	if err := store.AppendCheckReceipts(testCompleteCheckReceipts(jobID, treeSHA, now)); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(fmt.Sprintf("UPDATE %s SET passed = 0 WHERE job_id = ? AND required_check = ?", cicontract.CheckReceiptsTable), jobID, cicontract.RequiredCheckGate); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadCheckReceipts(jobID); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("LoadCheckReceipts() failure error = %v", err)
	}
	if _, err := database.Exec(fmt.Sprintf("DROP TABLE %s", cicontract.CheckReceiptsTable)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(fmt.Sprintf("DROP TABLE %s", cicontract.RefreshDeltasTable)); err != nil {
		t.Fatal(err)
	}
	if err := ensureRemoteCIAuthorityBindingSQLiteSchema(database); err != nil {
		t.Fatalf("ensureRemoteCIAuthorityBindingSQLiteSchema() error = %v", err)
	}
	if err := verifyDurationLedgerSQLAuthorityBindings(database); err != nil {
		t.Fatalf("verifyDurationLedgerSQLAuthorityBindings() error = %v", err)
	}
}

func TestDurationLedgerSQLiteMigratesEmptyLegacyWorkloadReuseTables(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE ci_run_workloads (job_id TEXT NOT NULL, workload_id TEXT NOT NULL, disposition TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err != nil {
		t.Fatalf("empty legacy workload reuse schema migration error = %v", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ci_run_workloads'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("retired reuse table count = %d, want 0", count)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_schema_migrations WHERE name='retire-workload-result-reuse-v1'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("retired reuse migration count = %d, want 1", count)
	}
}

func TestDurationLedgerSQLiteRefusesNonEmptyLegacyWorkloadReuseTables(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`CREATE TABLE ci_workload_pass_proofs (identity_digest TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_workload_pass_proofs(identity_digest) VALUES ('historical-pass')`); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil || !strings.Contains(err.Error(), "refuse to discard historical facts") {
		t.Fatalf("non-empty legacy workload reuse schema error = %v", err)
	}
}

func prepareRemoteCIAuthorityRecordTest(t *testing.T, store *DurationLedgerStore, jobID, attemptGeneration, treeSHA string) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`INSERT INTO ci_runs (
		job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, source_tree_sha,
		candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status,
		authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text
	) VALUES (?, 'manual-cli', 'local-fast', 'sha256:plan', 'sha256:catalog', '6', ?, ?, ?, 'runner', 'failed', 0, 1, 2, 1, '')`,
		jobID, treeSHA, "sha256:"+strings.Repeat("4", 64), "sha256:"+strings.Repeat("5", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`INSERT INTO ci_remote_baseline_refresh_lease (
		singleton, schema_version, attempt_generation, accepted_generation, accepted_state_sha256,
		target_generation, token, builder_job_id, target_tree_sha, phase, lease_expires_at_unix_ms, last_started_at_unix_ms
	) VALUES (1, 1, ?, '6', ?, '7', 'token', ?, ?, 'claimed', 2, 1)`, attemptGeneration, "sha256:"+strings.Repeat("1", 64), jobID, treeSHA); err != nil {
		t.Fatal(err)
	}
}

func testCompleteCheckReceipts(jobID, treeSHA string, startedAt time.Time) []CheckReceiptRecord {
	checks := cicontract.RequiredChecks()
	receipts := make([]CheckReceiptRecord, 0, len(checks))
	for index, check := range checks {
		start := startedAt.Add(time.Duration(index) * time.Minute)
		receipt := CheckReceiptRecord{
			RunID: jobID, JobID: jobID, CandidateTreeSHA: treeSHA, AcceptedGeneration: 6, AcceptedSnapshotID: "snapshot-6",
			RequiredCheck: check, Executed: true, Passed: true, StartedAt: start, CompletedAt: start.Add(time.Second), Duration: time.Second,
		}
		digest, err := CheckReceiptSHA256(receipt)
		if err != nil {
			panic(err)
		}
		receipt.ReceiptSHA256 = digest
		receipts = append(receipts, receipt)
	}
	return receipts
}
