package gate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	"golang.org/x/sync/errgroup"
)

const testRemoteCITimingWarningAgentDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestLiveRemoteCITimingWarningExactlyOnceAndFinalizerAbsorbs(t *testing.T) {
	store := newTimingWarningStore(t, 1)
	warning := validRemoteCITimingWarning("job-warning", "shard-a", 1)
	stored := assertInitialLiveRemoteCITimingWarning(t, store, warning)
	assertRetryLiveRemoteCITimingWarning(t, store, warning, stored)

	record := remoteCITimingWarningRunRecord(stored)
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("finalize live timing warning: %v", err)
	}
	loaded, err := store.LoadRemoteCIRun(record.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !remoteCITimingWarningSetsEqual(loaded.TimingWarnings, []RemoteCITimingWarning{warning}) ||
		len(loaded.Warnings) != 1 || loaded.Warnings[0] != warning.WarningText {
		t.Fatalf("loaded timing warning projection = %#v human=%#v", loaded.TimingWarnings, loaded.Warnings)
	}
	assertRemoteCITimingWarningRowCounts(t, store, record.JobID, 0, 1)
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatalf("idempotent final timing warning projection: %v", err)
	}
	assertRemoteCITimingWarningRowCounts(t, store, record.JobID, 0, 1)
}

func assertInitialLiveRemoteCITimingWarning(
	t *testing.T,
	store *DurationLedgerStore,
	warning RemoteCITimingWarning,
) RemoteCITimingWarning {
	t.Helper()
	stored, inserted, err := store.RecordLiveRemoteCITimingWarning(warning)
	if err != nil || !inserted || !remoteCITimingWarningEqual(stored, warning) {
		t.Fatalf("first live warning stored=%#v inserted=%t err=%v", stored, inserted, err)
	}
	return stored
}

func assertRetryLiveRemoteCITimingWarning(
	t *testing.T,
	store *DurationLedgerStore,
	warning RemoteCITimingWarning,
	want RemoteCITimingWarning,
) {
	t.Helper()
	retry := warning
	retry.WarningText = CanonicalRemoteCITimingWarningText(retry)
	stored, inserted, err := store.RecordLiveRemoteCITimingWarning(retry)
	if err != nil || inserted || !remoteCITimingWarningEqual(stored, want) {
		t.Fatalf("retry live warning stored=%#v inserted=%t err=%v", stored, inserted, err)
	}
}

func TestLiveRemoteCITimingWarningsAreConcurrentAndShardScoped(t *testing.T) {
	store := newTimingWarningStore(t, 1)
	warnings := []RemoteCITimingWarning{
		validRemoteCITimingWarning("job-concurrent", "shard-a", 1),
		validRemoteCITimingWarning("job-concurrent", "shard-b", 1),
	}
	var inserted atomic.Int64
	errorsByCall := make([]error, 16)
	var workers errgroup.Group
	for index := range errorsByCall {
		workers.Go(func() error {
			_, created, err := store.RecordLiveRemoteCITimingWarning(warnings[index%len(warnings)])
			errorsByCall[index] = err
			if created {
				inserted.Add(1)
			}
			return nil
		})
	}
	if err := workers.Wait(); err != nil {
		t.Fatalf("concurrent warning group error: %v", err)
	}
	for index, err := range errorsByCall {
		if err != nil {
			t.Fatalf("concurrent warning call %d: %v", index, err)
		}
	}
	if inserted.Load() != int64(len(warnings)) {
		t.Fatalf("inserted warnings=%d, want %d shard facts", inserted.Load(), len(warnings))
	}
	record := remoteCITimingWarningRunRecord(warnings[0])
	record.Shards = append(record.Shards, RemoteCIShardRecord{
		ShardIdentity: warnings[1].ShardIdentity, ContainerStatus: "Unknown",
		MaterializationTiming: ShardMaterializationTiming{Measurement: MaterializationMeasurementNotMeasured},
		Resources:             RemoteCIShardResources{},
	})
	record.Warnings = []string{warnings[0].WarningText, warnings[1].WarningText}
	record.TimingWarnings = warnings
	if err := store.RecordProvisionalRemoteCIRun(record); err != nil {
		t.Fatal(err)
	}
	assertRemoteCITimingWarningRowCounts(t, store, record.JobID, 0, 2)
}

func TestLiveRemoteCITimingWarningBatchCanonicalChunkedIdempotent(t *testing.T) {
	store := newTimingWarningStore(t, 1)
	warnings := batchRemoteCITimingWarnings()
	assertBatchInsertCanonical(t, store, warnings)
	assertBatchRevisionOnce(t, store)
	assertBatchMixedRetryAndNewWarning(t, store, warnings[0])
	assertBatchIdempotent(t, store, warnings)
	assertBatchEntryCancellation(t, store, warnings[0])
	assertCancelledBatchTransaction(t, store)
	assertCancelledBatchCommit(t, store)
	assertCancelledBusyBackoff(t)
}

func TestSQLiteWriteTransactionContextRetriesRealBusyLock(t *testing.T) {
	store := newTimingWarningStore(t, 1)
	locker, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer locker.Close()
	contender, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer contender.Close()
	if _, err := contender.Exec(`PRAGMA busy_timeout=1`); err != nil {
		t.Fatal(err)
	}
	var busyTimeout int
	if err := contender.QueryRow(`PRAGMA busy_timeout`).Scan(&busyTimeout); err != nil || busyTimeout != 1 {
		t.Fatalf("busy timeout=%d err=%v, want 1ms", busyTimeout, err)
	}
	lockTransaction, err := locker.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lockTransaction.Rollback()
	released := releaseSQLiteBusyLockAfterDelay(lockTransaction)
	err = withSQLiteWriteTransactionContext(context.Background(), contender, "real busy retry", func(transaction *sql.Tx) error {
		_, writeErr := transaction.Exec(`UPDATE ci_query_meta SET revision = revision WHERE singleton = 1`)
		return writeErr
	})
	assertSQLiteBusyRetrySucceeded(t, err, released)
}

func releaseSQLiteBusyLockAfterDelay(transaction *sql.Tx) <-chan error {
	released := make(chan error, 1)
	time.AfterFunc(40*time.Millisecond, func() { released <- transaction.Rollback() })
	return released
}

func assertSQLiteBusyRetrySucceeded(t *testing.T, retryErr error, released <-chan error) {
	t.Helper()
	if retryErr != nil {
		t.Fatalf("busy retry error=%v, want success after lock release", retryErr)
	}
	select {
	case err := <-released:
		if err != nil {
			t.Fatalf("release SQLite busy lock: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SQLite busy lock release did not finish")
	}
}

func batchRemoteCITimingWarnings() []RemoteCITimingWarning {
	warnings := make([]RemoteCITimingWarning, 0, remoteCITimingWarningInsertChunkSize+1)
	for index := 0; index <= remoteCITimingWarningInsertChunkSize; index++ {
		warnings = append(warnings, validRemoteCITimingWarning("job-batch", fmt.Sprintf("shard-%03d", index), 1))
	}
	return warnings
}

func batchRemoteCITimingWarningInput(warnings []RemoteCITimingWarning) []RemoteCITimingWarning {
	input := append([]RemoteCITimingWarning{warnings[remoteCITimingWarningInsertChunkSize]}, warnings[:remoteCITimingWarningInsertChunkSize]...)
	return append(input, warnings[0])
}

func assertBatchInsertCanonical(t *testing.T, store *DurationLedgerStore, warnings []RemoteCITimingWarning) {
	t.Helper()
	stored, inserted, err := store.RecordLiveRemoteCITimingWarnings(context.Background(), batchRemoteCITimingWarningInput(warnings))
	if err != nil || len(stored) != len(warnings) || len(inserted) != len(warnings) {
		t.Fatalf("batch stored=%d inserted=%d err=%v, want %d canonical rows", len(stored), len(inserted), err, len(warnings))
	}
	for index, value := range inserted {
		if !value || stored[index].ShardIdentity != fmt.Sprintf("shard-%03d", index) {
			t.Fatalf("batch[%d] = %#v inserted=%t, want canonical ordering and insert", index, stored[index], value)
		}
	}
	assertRemoteCITimingWarningRowCounts(t, store, "job-batch", len(warnings), 0)
}

func assertBatchRevisionOnce(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var revision string
	if err := database.QueryRow("SELECT revision FROM ci_query_meta WHERE singleton = 1").Scan(&revision); err != nil || revision != "1" {
		t.Fatalf("query revision=%q err=%v, want one batch advance", revision, err)
	}
}

func assertBatchMixedRetryAndNewWarning(t *testing.T, store *DurationLedgerStore, retry RemoteCITimingWarning) {
	t.Helper()
	added := validRemoteCITimingWarning(retry.JobID, "shard-new-wave", retry.AcceptedGeneration)
	added.EvidenceStartedAt = added.EvidenceStartedAt.Add(time.Minute)
	added.ObservedAt = added.ObservedAt.Add(time.Minute)
	added.WarningText = CanonicalRemoteCITimingWarningText(added)
	stored, inserted, err := store.RecordLiveRemoteCITimingWarnings(context.Background(), []RemoteCITimingWarning{added, retry})
	assertMixedBatchWriteResult(t, stored, inserted, err, retry, added)
	assertMixedBatchRevision(t, store, added)
}

func assertMixedBatchWriteResult(t *testing.T, stored []RemoteCITimingWarning, inserted []bool, err error, retry, added RemoteCITimingWarning) {
	t.Helper()
	if err != nil || len(stored) != 2 || len(inserted) != 2 {
		t.Fatalf("mixed batch stored=%d inserted=%d err=%v, want retry plus new warning", len(stored), len(inserted), err)
	}
	if inserted[0] || !inserted[1] || !stored[0].ObservedAt.Equal(retry.ObservedAt) || stored[1].ShardIdentity != added.ShardIdentity {
		t.Fatalf("mixed batch stored=%#v inserted=%#v, want first fact plus one insertion", stored, inserted)
	}
}

func assertMixedBatchRevision(t *testing.T, store *DurationLedgerStore, added RemoteCITimingWarning) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var revision string
	var updatedAtMS int64
	if err := database.QueryRow("SELECT revision, updated_at_unix_ms FROM ci_query_meta WHERE singleton = 1").Scan(&revision, &updatedAtMS); err != nil {
		t.Fatal(err)
	}
	if revision != "2" || updatedAtMS != added.ObservedAt.UnixMilli() {
		t.Fatalf("mixed batch revision=%q updated_at=%d, want revision 2 at %d", revision, updatedAtMS, added.ObservedAt.UnixMilli())
	}
}

func assertBatchIdempotent(t *testing.T, store *DurationLedgerStore, warnings []RemoteCITimingWarning) {
	t.Helper()
	stored, inserted, err := store.RecordLiveRemoteCITimingWarnings(context.Background(), batchRemoteCITimingWarningInput(warnings))
	if err != nil || len(stored) != len(warnings) {
		t.Fatalf("idempotent batch stored=%d err=%v", len(stored), err)
	}
	for index, value := range inserted {
		if value {
			t.Fatalf("idempotent batch inserted[%d]=true", index)
		}
	}
}

func assertBatchEntryCancellation(t *testing.T, store *DurationLedgerStore, warning RemoteCITimingWarning) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := store.RecordLiveRemoteCITimingWarnings(ctx, []RemoteCITimingWarning{warning}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled batch err=%v, want context.Canceled", err)
	}
}

func assertCancelledBatchTransaction(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	warning := validRemoteCITimingWarning("job-batch-cancel", "shard-cancel", 1)
	query := `INSERT INTO ci_live_timing_warnings (job_id, agent_token_digest, accepted_generation, scope, shard_identity, workload_id, evidence_kind, action, evidence_started_at_unix_ms, observed_at_unix_ms, evidence_duration_ms, target_ms, warning_text) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	ctx, cancel := context.WithCancel(context.Background())
	err = withSQLiteWriteTransactionContext(ctx, database, "cancelled batch", func(transaction *sql.Tx) error {
		cancel()
		_, execErr := transaction.ExecContext(ctx, query, remoteCITimingWarningSQLArguments(warning)...)
		if !errors.Is(execErr, context.Canceled) {
			return fmt.Errorf("cancelled ExecContext error = %v", execErr)
		}
		return execErr
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled transaction error = %v", err)
	}
	assertRemoteCITimingWarningRowCounts(t, store, warning.JobID, 0, 0)
}

func assertCancelledBatchCommit(t *testing.T, store *DurationLedgerStore) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	ctx, cancel := context.WithCancel(context.Background())
	err = withSQLiteWriteTransactionContext(ctx, database, "commit cancellation", func(transaction *sql.Tx) error {
		if _, execErr := transaction.ExecContext(ctx, `SELECT 1`); execErr != nil {
			return execErr
		}
		cancel()
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled commit error = %v", err)
	}
}

func assertCancelledBusyBackoff(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := waitSQLiteBusyRetry(ctx, sqliteWriteBusyRetryAttempts); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled busy retry error = %v", err)
	}
}

func TestLiveRemoteCITimingWarningSurvivesOtherRootCompactionBeforeFinalizer(t *testing.T) {
	store := newTimingWarningStore(t, 1)
	warning := validRemoteCITimingWarning("job-live-compaction", "shard-a", 1)
	if _, inserted, err := store.RecordLiveRemoteCITimingWarning(warning); err != nil || !inserted {
		t.Fatalf("record live timing warning inserted=%t err=%v", inserted, err)
	}
	seedAcceptedGenerationForTest(t, store, 2)
	sample := testDurationSample("timing-warning-compaction", testWorkloadDigest, true, 1)
	if _, err := store.AppendSamplesFast(2, []DurationSample{sample}); err != nil {
		t.Fatalf("trigger other-root compaction: %v", err)
	}
	assertRemoteCITimingWarningRowCounts(t, store, warning.JobID, 1, 0)
	if err := store.RecordProvisionalRemoteCIRun(remoteCITimingWarningRunRecord(warning)); err != nil {
		t.Fatalf("finalize warning after another root compaction: %v", err)
	}
	assertRemoteCITimingWarningRowCounts(t, store, warning.JobID, 0, 1)
}

func TestRemoteCITimingWarningFinalizerMismatchRollsBackAndPreservesLiveFact(t *testing.T) {
	store := newTimingWarningStore(t, 1)
	warning := validRemoteCITimingWarning("job-warning-rollback", "shard-a", 1)
	if _, _, err := store.RecordLiveRemoteCITimingWarning(warning); err != nil {
		t.Fatal(err)
	}
	drifted := warning
	drifted.EvidenceStartedAt = drifted.EvidenceStartedAt.Add(time.Millisecond)
	drifted.ObservedAt = drifted.ObservedAt.Add(time.Millisecond)
	drifted.WarningText = CanonicalRemoteCITimingWarningText(drifted)
	record := remoteCITimingWarningRunRecord(drifted)
	if err := store.RecordProvisionalRemoteCIRun(record); err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatalf("mismatched final timing warning error=%v, want conflict", err)
	}
	assertRemoteCITimingWarningRowCounts(t, store, warning.JobID, 1, 0)
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var runs int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ci_runs WHERE job_id = ?`, warning.JobID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("rolled-back finalizer left %d ci_runs rows", runs)
	}
}

func TestRemoteCITimingWarningValidationRejectsSemanticDrift(t *testing.T) {
	valid := validRemoteCITimingWarning("job-validation", "shard-a", 1)
	for name, mutate := range map[string]func(*RemoteCITimingWarning){
		"terminating action": func(warning *RemoteCITimingWarning) { warning.Action = "kill" },
		"false workload scope": func(warning *RemoteCITimingWarning) {
			warning.Scope = cicontract.TimingScopeWorkload
		},
		"local first poll": func(warning *RemoteCITimingWarning) {
			warning.ObservedAt = warning.EvidenceStartedAt.Add(cicontract.ShardTargetDuration - time.Millisecond)
			warning.EvidenceDurationMS = warning.ObservedAt.Sub(warning.EvidenceStartedAt).Milliseconds()
		},
		"wrong target": func(warning *RemoteCITimingWarning) { warning.TargetMS-- },
		"raw token":    func(warning *RemoteCITimingWarning) { warning.AgentTokenDigest = "secret" },
	} {
		t.Run(name, func(t *testing.T) {
			warning := valid
			mutate(&warning)
			warning.WarningText = CanonicalRemoteCITimingWarningText(warning)
			if err := warning.Validate(); err == nil {
				t.Fatal("expected timing warning semantic drift to be rejected")
			}
		})
	}
}

// TestFailedRemoteCITimingWarningValidationIgnoresOrphanPartialObservation 确保失败运行的原始耗时可完整留账，
// 但只有具备完整 execution 回执的 workload 才参与告警闭合；PASS 路径仍严格拒绝孤立观测。
func TestFailedRemoteCITimingWarningValidationIgnoresOrphanPartialObservation(t *testing.T) {
	startedAt := time.Date(2026, time.August, 5, 3, 0, 0, 0, time.UTC)
	profile := ExecutionProfile{
		CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured",
		StartupMS: 10, TestBodyMS: 20, TotalMS: 50,
	}
	execution := PlanGateExecution{
		GateID: "guard:complete", ShardIdentity: "shard-a", Status: ResultStatusPassed,
		StartedAt: startedAt, CompletedAt: startedAt.Add(50 * time.Millisecond), ExecutionProfile: profile,
	}
	cancelled := PlanGateExecution{
		GateID: "guard:cancelled", ShardIdentity: "shard-a", Status: ResultStatusCancelled,
		StartedAt: startedAt.Add(50 * time.Millisecond), CompletedAt: startedAt.Add(50 * time.Millisecond),
		ExecutionProfile: ExecutionProfile{CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured"},
	}
	orphanExecution := PlanGateExecution{
		GateID: "guard:orphan", ShardIdentity: "shard-a",
		ExecutionProfile: ExecutionProfile{
			CacheSource: "none", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured",
			TestBodyMS: 150_000, TotalMS: 150_000,
		},
	}
	orphanObservation := measuredWorkloadWarningObservation(
		"job-failed-orphan-observation", orphanExecution, cicontract.TimingTotal,
		startedAt, startedAt.Add(150*time.Second),
	)
	orphanWarning := workloadTimingWarningFromObservation(
		"job-failed-orphan-observation", testRemoteCITimingWarningAgentDigest, 1,
		orphanObservation, cicontract.TimingWarningEvidenceTotal,
	)
	record := RemoteCIRunRecord{
		JobID: "job-failed-orphan-observation", AgentTokenDigest: testRemoteCITimingWarningAgentDigest,
		AcceptedGeneration: 1, Status: ResultStatusFailed,
		Shards:             []RemoteCIShardRecord{{ShardIdentity: "shard-a"}},
		WorkloadExecutions: []PlanGateExecution{execution, cancelled},
		TimingObservations: []TimingObservation{
			measuredWorkloadWarningObservation("job-failed-orphan-observation", execution, cicontract.TimingTestBody, startedAt.Add(30*time.Millisecond), startedAt.Add(50*time.Millisecond)),
			measuredWorkloadWarningObservation("job-failed-orphan-observation", execution, cicontract.TimingTotal, startedAt, startedAt.Add(50*time.Millisecond)),
			orphanObservation,
		},
		TimingWarnings: []RemoteCITimingWarning{orphanWarning}, Warnings: []string{orphanWarning.WarningText},
	}
	if err := validateRemoteCIRunTimingWarnings(record); err != nil {
		t.Fatalf("failed provisional warning validation rejected orphan raw timing: %v", err)
	}
	record.Status = ResultStatusPassed
	record.WorkloadExecutions = []PlanGateExecution{execution}
	if err := validateRemoteCIRunTimingWarnings(record); err == nil || !strings.Contains(err.Error(), "has no execution") {
		t.Fatalf("PASS warning validation error = %v, want orphan observation rejection", err)
	}
}

func measuredWorkloadWarningObservation(jobID string, execution PlanGateExecution, phase cicontract.TimingPhase, startedAt, completedAt time.Time) TimingObservation {
	return TimingObservation{
		JobID: jobID, Scope: cicontract.TimingScopeWorkload,
		ShardIdentity: execution.ShardIdentity, WorkloadID: execution.GateID, Phase: phase,
		StartedAt: startedAt, CompletedAt: completedAt, DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw,
		CacheEvidence: NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile),
	}
}

func TestRemoteCITimingWarningValidationRejectsSQLiteTimestampRange(t *testing.T) {
	maxEvidenceStartedAtMS := int64(math.MaxInt64) - cicontract.ShardTargetDuration.Milliseconds()
	for name, testCase := range map[string]struct {
		startedAtMS int64
		wantError   string
	}{
		"epoch": {
			startedAtMS: 0,
			wantError:   "evidence_started_at Unix milliseconds must be > 0",
		},
		"pre-epoch": {
			startedAtMS: -1,
			wantError:   "evidence_started_at Unix milliseconds must be > 0",
		},
		"sqlite addition overflow": {
			startedAtMS: maxEvidenceStartedAtMS + 1,
			wantError:   fmt.Sprintf("evidence_started_at Unix milliseconds must be <= %d", maxEvidenceStartedAtMS),
		},
	} {
		t.Run(name, func(t *testing.T) {
			warning := validRemoteCITimingWarning("job-validation-"+name, "shard-a", 1)
			warning.EvidenceStartedAt = time.UnixMilli(testCase.startedAtMS).UTC()
			warning.WarningText = CanonicalRemoteCITimingWarningText(warning)
			if err := warning.Validate(); err == nil || !strings.Contains(err.Error(), testCase.wantError) {
				t.Fatalf("Validate() error = %v, want %q", err, testCase.wantError)
			}
		})
	}
}

func TestTimingWarningSchemaPreflightRejectsMalformedTableWithoutWriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "duration-ledger.sqlite")
	createMalformedTimingWarningSchema(t, path)
	store, err := NewDurationLedgerStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.openSQLiteAuthority(false); err == nil {
		t.Fatalf("malformed timing warning preflight error=%v", err)
	} else if !strings.Contains(err.Error(), "incompatible authority schema") &&
		!strings.Contains(err.Error(), "schema version 2 is unsupported") {
		t.Fatalf("malformed timing warning preflight error=%v", err)
	}
	assertMalformedTimingWarningSchemaUnchanged(t, path)
}

func createMalformedTimingWarningSchema(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.Exec(`PRAGMA user_version = 2; CREATE TABLE ci_live_timing_warnings (job_id TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func assertMalformedTimingWarningSchemaUnchanged(t *testing.T, path string) {
	t.Helper()
	database, err := sql.Open("sqlite", durationLedgerSQLiteDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	var columns, version int
	if err := database.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ci_live_timing_warnings')`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if columns != 1 || version != 2 {
		t.Fatalf("preflight mutated malformed authority columns=%d version=%d", columns, version)
	}
}

func newTimingWarningStore(t *testing.T, generation uint64) *DurationLedgerStore {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, generation)
	return store
}

func validRemoteCITimingWarning(jobID, shardIdentity string, generation uint64) RemoteCITimingWarning {
	startedAt := time.Date(2026, time.August, 3, 1, 0, 0, 0, time.UTC)
	warning := RemoteCITimingWarning{
		JobID: jobID, AgentTokenDigest: testRemoteCITimingWarningAgentDigest,
		AcceptedGeneration: generation, Scope: cicontract.TimingScopeShard,
		ShardIdentity: shardIdentity, EvidenceKind: cicontract.TimingWarningEvidenceRunning,
		Action:            cicontract.TimingWarningWarnAndContinue,
		EvidenceStartedAt: startedAt, ObservedAt: startedAt.Add(cicontract.ShardTargetDuration),
		EvidenceDurationMS: cicontract.ShardTargetDuration.Milliseconds(),
		TargetMS:           cicontract.ShardTargetDuration.Milliseconds(),
	}
	warning.WarningText = CanonicalRemoteCITimingWarningText(warning)
	return warning
}

func remoteCITimingWarningRunRecord(warning RemoteCITimingWarning) RemoteCIRunRecord {
	return RemoteCIRunRecord{
		JobID: warning.JobID, AgentTokenDigest: warning.AgentTokenDigest,
		Entrypoint: CIEntrypointManualCLI, Profile: ProfileLocalFast,
		PlanDigest: "sha256:plan", CatalogDigest: "sha256:" + strings.Repeat("b", 64),
		AcceptedGeneration: warning.AcceptedGeneration, ImageCacheSnapshotID: fmt.Sprintf("snapshot-%d", warning.AcceptedGeneration),
		SourceTreeSHA: strings.Repeat("c", 40), CandidateGateSourceSHA256: "sha256:" + strings.Repeat("d", 64),
		CandidateGateToolchainSHA256: "sha256:" + strings.Repeat("e", 64), RunnerImage: "runner@example",
		Platform: cicontract.TargetPlatform, RunnerIdentityDigest: "sha256:" + strings.Repeat("f", 64),
		ToolchainDigest: "sha256:" + strings.Repeat("0", 64), ExecutionMode: DurationExecutionModeNormal,
		Status: ResultStatusFailed, StartedAt: warning.EvidenceStartedAt, CompletedAt: warning.ObservedAt,
		Shards: []RemoteCIShardRecord{{
			ShardIdentity: warning.ShardIdentity, ContainerStatus: "Unknown",
			MaterializationTiming: ShardMaterializationTiming{Measurement: MaterializationMeasurementNotMeasured},
			Resources:             RemoteCIShardResources{},
		}},
		Warnings: []string{warning.WarningText}, TimingWarnings: []RemoteCITimingWarning{warning},
	}
}

func assertRemoteCITimingWarningRowCounts(t *testing.T, store *DurationLedgerStore, jobID string, wantLive, wantFinal int) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for table, want := range map[string]int{
		cicontract.LiveTimingWarningsTable: wantLive,
		cicontract.RunTimingWarningsTable:  wantFinal,
	} {
		var count int
		if err := database.QueryRow(`SELECT COUNT(*) FROM `+table+` WHERE job_id = ?`, jobID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != want {
			t.Fatalf("%s rows=%d, want %d", table, count, want)
		}
	}
}
