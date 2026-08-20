package gate

import (
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	sqlitedriver "modernc.org/sqlite"
)

func TestLoadSQLiteNormalResourceTierReservesKeyedByJobAndShard(t *testing.T) {
	tests := []struct {
		name      string
		resources []RemoteCIShardResources
		want      []keyedReserveTierExpectation
	}{
		{
			name: "same tier aggregates both jobs",
			resources: []RemoteCIShardResources{
				{ClassID: "small", CPU: 2, MemoryGiB: 4},
				{ClassID: "small", CPU: 2, MemoryGiB: 4},
			},
			want: []keyedReserveTierExpectation{{
				tier: cicontract.WorkloadResourceTierFast, sampleCount: 2, p95MS: 5_000,
			}},
		},
		{
			name: "different tiers stay in separate buckets",
			resources: []RemoteCIShardResources{
				{ClassID: "small", CPU: 2, MemoryGiB: 4},
				{ClassID: "medium", CPU: 4, MemoryGiB: 8},
			},
			want: []keyedReserveTierExpectation{
				{tier: cicontract.WorkloadResourceTierFast, sampleCount: 1, p95MS: 5_000},
				{tier: cicontract.WorkloadResourceTierMedium, sampleCount: 1, p95MS: 5_000},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runKeyedNormalReserveCase(t, test.resources, test.want)
		})
	}
}

func runKeyedNormalReserveCase(t *testing.T, resources []RemoteCIShardResources, want []keyedReserveTierExpectation) {
	t.Helper()
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	seedSQLiteKeyedNormalReserveEvidence(t, store, resources)
	reserves, queryCount, err := loadKeyedSQLiteNormalResourceTierReserves(t, store)
	if err != nil {
		t.Fatal(err)
	}
	if queryCount != 1 {
		t.Fatalf("keyed normal reserve query count = %d, want one set-based query", queryCount)
	}
	if len(reserves) != len(want) {
		t.Fatalf("keyed normal reserve buckets = %#v, want %#v", reserves, want)
	}
	for index, expected := range want {
		reserve := reserves[index]
		if reserve.ResourceTier != expected.tier || reserve.Measurement == nil {
			t.Fatalf("reserve[%d] = %#v, want measured tier %d", index, reserve, expected.tier)
		}
		if reserve.Measurement.SampleCount != expected.sampleCount || reserve.Measurement.P95MS != expected.p95MS {
			t.Fatalf("reserve[%d] measurement = %#v, want sample_count=%d p95=%dms", index, reserve.Measurement, expected.sampleCount, expected.p95MS)
		}
	}
}

func TestLoadSQLiteNormalResourceTierReservesRejectsConflictingResourceEvidence(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	seedSQLiteKeyedNormalReserveEvidence(t, store, []RemoteCIShardResources{{ClassID: "small", CPU: 2, MemoryGiB: 4}})
	overhead := keyedReserveTestOverhead()
	database := openWorkloadPassDatabase(t, store)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close conflicting-resource SQLite fixture: %v", err)
		}
	})
	queryer := conflictingSQLiteReserveQueryer{database: database}
	planning := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	if _, _, err := loadSQLiteNormalReserveEvidence(queryer, 1, planning, overhead.CalibrationResourceClassID); err == nil || !strings.Contains(err.Error(), "conflicting resources") {
		t.Fatalf("conflicting resource evidence error = %v, want fail-fast", err)
	}
}

type keyedReserveTierExpectation struct {
	tier        cicontract.WorkloadResourceTier
	sampleCount int
	p95MS       int64
}

func seedSQLiteKeyedNormalReserveEvidence(t *testing.T, store *DurationLedgerStore, resources []RemoteCIShardResources) {
	t.Helper()
	database := openWorkloadPassDatabase(t, store)
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close keyed-resource SQLite fixture: %v", err)
		}
	})
	overhead := keyedReserveTestOverhead()
	base := time.UnixMilli(10_000).UTC()
	const sharedShard = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	for index, resource := range resources {
		jobID := fmt.Sprintf("keyed-reserve-job-%c", 'a'+index)
		insertGenerationOneRunForTest(t, database, jobID)
		if _, err := database.Exec(`UPDATE ci_runs SET image_cache_snapshot_id = ?, started_at_unix_ms = ?, completed_at_unix_ms = ? WHERE job_id = ?`, overhead.AcceptedSnapshotID, base.UnixMilli(), base.Add(10*time.Second).UnixMilli(), jobID); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Exec(`INSERT INTO ci_remote_run_execution_identities (job_id, platform, runner_identity_digest, toolchain_digest, execution_mode) VALUES (?, ?, ?, ?, ?)`, jobID, overhead.Platform, overhead.Runner, overhead.Toolchain, DurationExecutionModeNormal); err != nil {
			t.Fatal(err)
		}
		transaction, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		record := RemoteCIRunRecord{JobID: jobID, Status: ResultStatusPassed, Authoritative: true, ExecutionMode: DurationExecutionModeNormal}
		shard := RemoteCIShardRecord{ShardIdentity: sharedShard, ContainerGroup: "eci-" + jobID, ContainerStatus: "Succeeded", MaterializationTiming: measuredShardMaterializationTiming(sharedShard), Resources: resource}
		if err := insertSQLiteRemoteCIRunShard(transaction, record, shard); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		observations := normalReserveTimingObservations(jobID, sharedShard, base.Add(time.Duration(index)*time.Minute), resource)
		if err := replaceSQLiteTimingObservations(transaction, jobID, observations); err != nil {
			_ = transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func loadKeyedSQLiteNormalResourceTierReserves(t *testing.T, store *DurationLedgerStore) ([]cicontract.WorkloadResourceTierReserve, int64, error) {
	t.Helper()
	overhead := keyedReserveTestOverhead()
	var queryCount atomic.Int64
	driverName := fmt.Sprintf("sqlite-keyed-resource-reserve-query-count-%p", &queryCount)
	sql.Register(driverName, &resourceReserveQueryCountingDriver{base: &sqlitedriver.Driver{}, count: &queryCount})
	database, err := sql.Open(driverName, durationLedgerSQLiteDSN(store.path))
	if err != nil {
		return nil, 0, err
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close keyed-resource query-count database: %v", err)
		}
	})
	transaction, err := database.Begin()
	if err != nil {
		return nil, 0, err
	}
	defer transaction.Rollback()
	queryCount.Store(0)
	planning := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	reserves, err := loadSQLiteNormalResourceTierReserves(transaction, 1, planning, &overhead)
	if err != nil {
		return nil, queryCount.Load(), err
	}
	if err := transaction.Commit(); err != nil {
		return nil, queryCount.Load(), err
	}
	return reserves, queryCount.Load(), nil
}

func keyedReserveTestOverhead() ShardOrchestrationOverhead {
	return normalReserveTestOverhead("sha256:" + strings.Repeat("a", 64))
}

type conflictingSQLiteReserveQueryer struct {
	database *sql.DB
}

func (queryer conflictingSQLiteReserveQueryer) Query(query string, args ...any) (*sql.Rows, error) {
	replaced := strings.Replace(query, "runs.job_id, shards.resources_json,", `runs.job_id, CASE WHEN timing.phase = 'eci_wait' THEN '{"class_id":"medium","cpu":4,"memory_gib":8}' ELSE '{"class_id":"small","cpu":2,"memory_gib":4}' END,`, 1)
	if replaced == query {
		return nil, fmt.Errorf("test query did not expose shard resources projection")
	}
	return queryer.database.Query(replaced, args...)
}

func (queryer conflictingSQLiteReserveQueryer) QueryRow(query string, args ...any) *sql.Row {
	return queryer.database.QueryRow(query, args...)
}
