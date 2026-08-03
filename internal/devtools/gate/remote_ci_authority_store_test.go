package gate

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/sync/errgroup"
)

func TestRemoteCIAuthorityRecordsRoundTripAndFailFast(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 6)
	const (
		jobID      = "refresh-job"
		generation = "7"
		treeSHA    = "6666666666666666666666666666666666666666"
	)
	record := prepareRemoteCIAuthorityRecordTest(t, store, jobID, generation, treeSHA)
	receipts := completeWorkloadPassReceipts(t, record)
	testCheckReceiptRoundTripAndFailFast(t, store, record, receipts)
	testCheckReceiptValidation(t, receipts)
}

// TestSharedSQLiteConcurrentJobReceiptsKeepAgentTokensIsolated 验证普通 CI 回执写入共享
// WAL 权威而无进程级准入锁；刷新单例刻意不参与该路径。
func TestSharedSQLiteConcurrentJobReceiptsKeepAgentTokensIsolated(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 6)
	now := time.Date(2026, time.August, 3, 11, 0, 0, 0, time.UTC)
	jobs := prepareConcurrentReceiptJobs(t, store)
	if err := appendConcurrentJobReceipts(store, jobs, now); err != nil {
		t.Fatalf("concurrent receipt write: %v", err)
	}
	assertConcurrentJobReceiptIsolation(t, store, jobs)
}

type concurrentReceiptJob struct {
	jobID, treeSHA, tokenDigest string
	record                      RemoteCIRunRecord
}

func prepareConcurrentReceiptJobs(t *testing.T, store *DurationLedgerStore) []concurrentReceiptJob {
	t.Helper()
	jobs := make([]concurrentReceiptJob, 4)
	for index := range jobs {
		jobID := fmt.Sprintf("concurrent-job-%d", index)
		record, _, _ := recordWorkloadPassRun(t, store, jobID, 6, "concurrent-authority-"+jobID)
		jobs[index] = concurrentReceiptJob{jobID: jobID, treeSHA: record.SourceTreeSHA, tokenDigest: record.AgentTokenDigest, record: record}
	}
	return jobs
}

func appendConcurrentJobReceipts(store *DurationLedgerStore, jobs []concurrentReceiptJob, now time.Time) error {
	var group errgroup.Group
	for _, job := range jobs {
		group.Go(func() error {
			receipts, err := concurrentJobReceipts(job, now)
			if err != nil {
				return err
			}
			return store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(job.record), receipts, nil, false)
		})
	}
	return group.Wait()
}

func concurrentJobReceipts(job concurrentReceiptJob, now time.Time) ([]CheckReceiptRecord, error) {
	receipts := testCompleteCheckReceipts(job.jobID, job.treeSHA, now)
	for index := range receipts {
		receipts[index].AgentTokenDigest = job.tokenDigest
		digest, err := CheckReceiptSHA256(receipts[index])
		if err != nil {
			return nil, err
		}
		receipts[index].ReceiptSHA256 = digest
	}
	return receipts, nil
}

func assertConcurrentJobReceiptIsolation(t *testing.T, store *DurationLedgerStore, jobs []concurrentReceiptJob) {
	t.Helper()
	for _, job := range jobs {
		receipts, err := store.LoadCheckReceipts(job.jobID)
		if err != nil {
			t.Fatalf("load %s receipts: %v", job.jobID, err)
		}
		for _, receipt := range receipts {
			if receipt.JobID != job.jobID || receipt.AgentTokenDigest != job.tokenDigest {
				t.Fatalf("receipt cross-talk: %#v", receipt)
			}
		}
	}
}

func testCheckReceiptRoundTripAndFailFast(t *testing.T, store *DurationLedgerStore, record RemoteCIRunRecord, receipts []CheckReceiptRecord) {
	t.Helper()
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err != nil {
		t.Fatalf("FinalizeRemoteCIRunAuthorityWithSamples() error = %v", err)
	}
	if err := store.FinalizeRemoteCIRunAuthorityWithSamples(remoteCIRunAuthorityIdentity(record), receipts, nil, false); err == nil {
		t.Fatal("duplicate remote CI authority finalization was accepted")
	}
	loaded, err := store.LoadCheckReceipts(record.JobID)
	if err != nil {
		t.Fatalf("LoadCheckReceipts() error = %v", err)
	}
	if !reflect.DeepEqual(loaded, receipts) {
		t.Fatalf("check receipts = %#v, want %#v", loaded, receipts)
	}
}

func testCheckReceiptValidation(t *testing.T, receipts []CheckReceiptRecord) {
	t.Helper()
	testCheckReceiptIdentityValidation(t, receipts)
	testCheckReceiptReuseValidation(t, receipts)
}

func testCheckReceiptIdentityValidation(t *testing.T, receipts []CheckReceiptRecord) {
	t.Helper()
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
	receiptSHA256, err := CheckReceiptSHA256(maxGenerationReceipt)
	if err != nil {
		t.Fatal(err)
	}
	maxGenerationReceipt.ReceiptSHA256 = receiptSHA256
	if err := validateCheckReceiptRecord(maxGenerationReceipt); err != nil {
		t.Fatalf("maximum uint64 check receipt generation error = %v", err)
	}
	tampered := receipts[0]
	tampered.CompletedAt = tampered.CompletedAt.Add(time.Second)
	tampered.Duration = tampered.CompletedAt.Sub(tampered.StartedAt)
	if err := validateCheckReceiptRecord(tampered); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("tampered check receipt error = %v", err)
	}
}

func testCheckReceiptReuseValidation(t *testing.T, receipts []CheckReceiptRecord) {
	t.Helper()
	var err error
	reused := receipts[0]
	reused.Executed, reused.Reused = false, true
	reused.ReuseProofSHA256 = "sha256:" + strings.Repeat("b", 64)
	reused.ReceiptSHA256, err = CheckReceiptSHA256(reused)
	if err != nil || validateCheckReceiptRecord(reused) != nil {
		t.Fatalf("reused receipt validation = hash %v validate %v", err, validateCheckReceiptRecord(reused))
	}
	missingProof := reused
	missingProof.ReuseProofSHA256 = ""
	missingProof.ReceiptSHA256, err = CheckReceiptSHA256(missingProof)
	if err != nil || validateCheckReceiptRecord(missingProof) == nil {
		t.Fatal("reused receipt without proof was accepted")
	}
	mixed := reused
	mixed.Executed = true
	mixed.ReceiptSHA256, err = CheckReceiptSHA256(mixed)
	if err != nil || validateCheckReceiptRecord(mixed) != nil {
		t.Fatalf("mixed receipt validation = hash %v validate %v", err, validateCheckReceiptRecord(mixed))
	}
	nonReuseProof := receipts[0]
	nonReuseProof.ReuseProofSHA256 = reused.ReuseProofSHA256
	nonReuseProof.ReceiptSHA256, err = CheckReceiptSHA256(nonReuseProof)
	if err != nil || validateCheckReceiptRecord(nonReuseProof) == nil {
		t.Fatal("non-reused receipt carrying proof was accepted")
	}
}

func TestLoadCheckReceiptsRejectsStoredFailureAndSchemaRejectsMissingTables(t *testing.T) {
	store := newWorkloadPassEvidenceStore(t, 6)
	const jobID = "receipt-job"
	treeSHA := strings.Repeat("6", 40)
	record := prepareRemoteCIAuthorityRecordTest(t, store, jobID, "9", treeSHA)
	testCheckReceiptRoundTripAndFailFast(t, store, record, completeWorkloadPassReceipts(t, record))
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	testLoadCheckReceiptsRejectsStoredFailure(t, store, database, jobID)
	testRemoteCIAuthoritySchemaRejectsMissingTables(t, database)
}

func testLoadCheckReceiptsRejectsStoredFailure(t *testing.T, store *DurationLedgerStore, database *sql.DB, jobID string) {
	t.Helper()
	if _, err := database.Exec(fmt.Sprintf("UPDATE %s SET passed = 0 WHERE job_id = ? AND required_check = ?", cicontract.CheckReceiptsTable), jobID, cicontract.RequiredCheckGate); err != nil {
		t.Fatal(err)
	}
	if _, err := store.LoadCheckReceipts(jobID); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("LoadCheckReceipts() failure error = %v", err)
	}
}

func testRemoteCIAuthoritySchemaRejectsMissingTables(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(fmt.Sprintf("DROP TABLE %s", cicontract.CheckReceiptsTable)); err != nil {
		t.Fatal(err)
	}
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil {
		t.Fatal("partial authority schema was accepted by the sole canonical schema writer")
	}
}

func TestDurationLedgerSQLiteRejectsEmptyLegacyWorkloadReuseTables(t *testing.T) {
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
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil {
		t.Fatalf("empty legacy workload reuse schema error = %v, want fail-fast refusal", err)
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='ci_run_workloads'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("rejected legacy reuse table count = %d, want 1", count)
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
	if err := ensureDurationLedgerSQLiteSchema(database, time.Now); err == nil {
		t.Fatalf("non-empty legacy workload reuse schema error = %v", err)
	}
}

func prepareRemoteCIAuthorityRecordTest(t *testing.T, store *DurationLedgerStore, jobID, _ string, treeSHA string) RemoteCIRunRecord {
	t.Helper()
	record, _, _ := recordWorkloadPassRun(t, store, jobID, 6, "authority-"+jobID)
	if record.SourceTreeSHA != treeSHA {
		t.Fatalf("provisional remote CI run tree = %q, want %q", record.SourceTreeSHA, treeSHA)
	}
	return record
}

func testCompleteCheckReceipts(jobID, treeSHA string, startedAt time.Time) []CheckReceiptRecord {
	checks := cicontract.RequiredChecks()
	receipts := make([]CheckReceiptRecord, 0, len(checks))
	for index, check := range checks {
		start := startedAt.Add(time.Duration(index) * time.Minute)
		receipt := CheckReceiptRecord{
			RunID: jobID, JobID: jobID, CandidateTreeSHA: treeSHA, AgentTokenDigest: "sha256:" + strings.Repeat("a", 64), AcceptedGeneration: 6, AcceptedSnapshotID: "snapshot-6",
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
