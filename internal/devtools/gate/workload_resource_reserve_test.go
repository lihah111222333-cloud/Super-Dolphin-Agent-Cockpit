package gate

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
	sqlitedriver "modernc.org/sqlite"
)

// TestNormalPlanningRequiresResourceTierReserve locks the fail-fast boundary
// before normal planning can consume an unscoped calibration overhead.
func TestNormalPlanningRequiresResourceTierReserve(t *testing.T) {
	context := testLinuxPlanningContext()
	context.ResourceTierReserves = nil
	context.ShardOverheadP95MS = 1_000
	context.ShardOverheadSampleCount = 1
	context.ShardOverheadProvenanceDigest = "sha256:" + strings.Repeat("a", 64)
	if _, err := context.EffectiveTargetDurationMS(); err == nil || !strings.Contains(err.Error(), "resource-tier reserve") {
		t.Fatalf("normal planning accepted unscoped overhead: %v", err)
	}
}

func TestDeriveNormalResourceTierReservesExcludesWorkloadAndTestBinaryCompileUnion(t *testing.T) {
	base := time.UnixMilli(10_000).UTC()
	resources := []RemoteCIShardResources{
		{ClassID: "small", CPU: 2, MemoryGiB: 4},
		{ClassID: "medium", CPU: 4, MemoryGiB: 8},
		{ClassID: "maximum", CPU: 8, MemoryGiB: 16},
	}
	var observations []TimingObservation
	var shards []RemoteCIShardRecord
	for index, resource := range resources {
		jobID, shardID := "job-"+string(rune('a'+index)), "shard-"+string(rune('a'+index))
		observations = append(observations, normalReserveTimingObservations(jobID, shardID, base, resource)...)
		shards = append(shards, RemoteCIShardRecord{ShardIdentity: shardID, Resources: resource})
	}
	reserves, err := DeriveNormalResourceTierReserves(observations, shards, "calibration")
	if err != nil {
		t.Fatal(err)
	}
	for index, reserve := range reserves {
		wantTier := cicontract.WorkloadResourceTier(index + 1)
		if reserve.ResourceTier != wantTier || reserve.P95MS != 5_000 || reserve.SampleCount != 1 || reserve.ProvenanceDigest == "" {
			t.Fatalf("reserve[%d] = %#v, want tier %d measured union P95 5000ms", index, reserve, wantTier)
		}
	}
}

func TestLoadPlanningDerivesNormalResourceTierReservesFromSQLiteTiming(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	seedSQLiteNormalReserveEvidence(t, store, time.UnixMilli(10_000).UTC())
	identity := compileTimingTestIdentity()
	identity.Platform, identity.RunnerIdentityDigest, identity.ToolchainDigest = overhead.Platform, overhead.Runner, overhead.Toolchain
	seedSQLiteNormalReserveCompileIdentities(t, store, time.UnixMilli(10_000).UTC(), identity)
	context := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	snapshot, err := store.LoadPlanning(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ResourceTierReserves) != 3 || snapshot.SampleIndex == nil {
		t.Fatalf("SQLite planning reserves/index = %#v/%v", snapshot.ResourceTierReserves, snapshot.SampleIndex)
	}
	target, err := snapshot.SampleIndex.context.EffectiveTargetDurationForResourceTier(cicontract.WorkloadResourceTierMedium)
	if err != nil || target != 95_000 {
		t.Fatalf("SQLite planning medium target = %d, %v; want 95000", target, err)
	}
}

func TestLoadPlanningRejectsNormalReserveFromDifferentRunnerToolchain(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
	targetRunner := "sha256:" + strings.Repeat("d", 64)
	targetToolchain := "sha256:" + strings.Repeat("e", 64)
	overhead.Runner, overhead.Toolchain = targetRunner, targetToolchain
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	base := time.UnixMilli(10_000).UTC()
	seedSQLiteNormalReserveEvidence(t, store, base)
	seedSQLiteNormalReserveCompileIdentities(t, store, base, compileTimingTestIdentity())
	context := PlanningContext{Platform: overhead.Platform, Runner: targetRunner, Toolchain: targetToolchain, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	if _, err := store.LoadPlanning(context); err == nil {
		t.Fatal("normal planning reused resource-tier reserve from a different runner/toolchain")
	}
}

func TestLoadPlanningRejectsNormalReserveWithMixedCompileIdentity(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	base := time.UnixMilli(10_000).UTC()
	seedSQLiteNormalReserveEvidence(t, store, base)
	identity := compileTimingTestIdentity()
	identity.Platform, identity.RunnerIdentityDigest, identity.ToolchainDigest = overhead.Platform, overhead.Runner, overhead.Toolchain
	seedSQLiteNormalReserveCompileIdentities(t, store, base, identity)
	mixed := compileTimingTestObservation(base.Add(4*time.Second), 100)
	mixed.Identity = identity
	mixed.Identity.RunnerIdentityDigest = "different-runner"
	mixed.Identity.ResourceClassID, mixed.Identity.ResourceCPU, mixed.Identity.ResourceMemoryGiB = "medium", 4, 8
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := insertSQLiteCompileTimingObservation(transaction, "reserve-job-b", mixed); err != nil {
		transaction.Rollback()
		database.Close()
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	context := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	if _, err := store.LoadPlanning(context); err == nil {
		t.Fatal("normal planning reused resource-tier reserve with mixed compile identity")
	}
}

func TestLoadSQLiteNormalResourceTierReservesUsesOneSetBasedQuery(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(sample.ProvenanceDigest)
	base := time.UnixMilli(10_000).UTC()
	seedSQLiteNormalReserveEvidence(t, store, base)
	identity := compileTimingTestIdentity()
	identity.Platform, identity.RunnerIdentityDigest, identity.ToolchainDigest = overhead.Platform, overhead.Runner, overhead.Toolchain
	seedSQLiteNormalReserveCompileIdentities(t, store, base, identity)

	var queryCount atomic.Int64
	driverName := fmt.Sprintf("sqlite-resource-reserve-query-count-%p", &queryCount)
	sql.Register(driverName, &resourceReserveQueryCountingDriver{base: &sqlitedriver.Driver{}, count: &queryCount})
	database, err := sql.Open(driverName, durationLedgerSQLiteDSN(store.path))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	queryCount.Store(0)
	planning := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	reserves, err := loadSQLiteNormalResourceTierReserves(transaction, 1, planning, &overhead)
	if err != nil {
		t.Fatal(err)
	}
	if len(reserves) != 3 {
		t.Fatalf("reserve count = %d, want 3", len(reserves))
	}
	if got := queryCount.Load(); got != 1 {
		t.Fatalf("normal reserve query count = %d, want one set-based query", got)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	retained := retainedWorkloadPassGenerations(1)
	details := sqliteQueryPlanDetails(t, database, sqliteNormalReserveEvidenceQuery,
		retained[0], retained[1], retained[2], overhead.AcceptedSnapshotID,
		planning.Platform, planning.Runner, planning.Toolchain, DurationExecutionModeNormal,
		planning.Platform, planning.Runner, planning.Toolchain, DurationExecutionModeNormal,
		overhead.CalibrationResourceClassID)
	assertSQLiteQueryPlanAccess(t, details, []string{
		"idx_ci_runs_accepted_generation", "sqlite_autoindex_ci_compile_timing_observations_1",
		"idx_ci_timing_observations_compile_group", "sqlite_autoindex_ci_shards_1",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"runs", "shards", "timing", "exact_identity", "mixed_identity"})
}

type resourceReserveQueryCountingDriver struct {
	base  driver.Driver
	count *atomic.Int64
}

func (countingDriver *resourceReserveQueryCountingDriver) Open(name string) (driver.Conn, error) {
	connection, err := countingDriver.base.Open(name)
	if err != nil {
		return nil, err
	}
	return &resourceReserveQueryCountingConn{Conn: connection, count: countingDriver.count}, nil
}

type resourceReserveQueryCountingConn struct {
	driver.Conn
	count *atomic.Int64
}

func (connection *resourceReserveQueryCountingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	connection.count.Add(1)
	queryer, ok := connection.Conn.(driver.QueryerContext)
	if !ok {
		return nil, fmt.Errorf("SQLite query-count connection %T lacks QueryerContext", connection.Conn)
	}
	return queryer.QueryContext(ctx, query, args)
}

func seedSQLiteNormalReserveCompileIdentities(t *testing.T, store *DurationLedgerStore, base time.Time, identity CompileTimingIdentity) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	resources := []RemoteCIShardResources{{ClassID: "small", CPU: 2, MemoryGiB: 4}, {ClassID: "medium", CPU: 4, MemoryGiB: 8}, {ClassID: "maximum", CPU: 8, MemoryGiB: 16}}
	for index, resource := range resources {
		observation := compileTimingTestObservation(base.Add(time.Duration(index)*time.Second), 100)
		observation.Identity = identity
		observation.Identity.ResourceClassID = resource.ClassID
		observation.Identity.ResourceCPU = resource.CPU
		observation.Identity.ResourceMemoryGiB = resource.MemoryGiB
		if err := insertSQLiteCompileTimingObservation(transaction, "reserve-job-"+string(rune('a'+index)), observation); err != nil {
			transaction.Rollback()
			t.Fatal(err)
		}
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
}

func seedSQLiteNormalReserveEvidence(t *testing.T, store *DurationLedgerStore, base time.Time) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	resources := []RemoteCIShardResources{{ClassID: "small", CPU: 2, MemoryGiB: 4}, {ClassID: "medium", CPU: 4, MemoryGiB: 8}, {ClassID: "maximum", CPU: 8, MemoryGiB: 16}}
	for index, resource := range resources {
		jobID := "reserve-job-" + string(rune('a'+index))
		shardID := "sha256:" + strings.Repeat(string(rune('a'+index)), 64)
		insertGenerationOneRunForTest(t, database, jobID)
		if _, err := database.Exec(`UPDATE ci_runs SET image_cache_snapshot_id = 'snapshot-1', started_at_unix_ms = ?, completed_at_unix_ms = ? WHERE job_id = ?`, base.UnixMilli(), base.Add(10*time.Second).UnixMilli(), jobID); err != nil {
			t.Fatal(err)
		}
		transaction, err := database.Begin()
		if err != nil {
			t.Fatal(err)
		}
		record := RemoteCIRunRecord{JobID: jobID, Status: ResultStatusPassed, Authoritative: true}
		shard := RemoteCIShardRecord{ShardIdentity: shardID, ContainerGroup: "eci-" + jobID, ContainerStatus: "Succeeded", MaterializationTiming: measuredShardMaterializationTiming(shardID), Resources: resource}
		if err := insertSQLiteRemoteCIRunShard(transaction, record, shard); err != nil {
			transaction.Rollback()
			t.Fatal(err)
		}
		if err := replaceSQLiteTimingObservations(transaction, jobID, normalReserveTimingObservations(jobID, shardID, base, resource)); err != nil {
			transaction.Rollback()
			t.Fatal(err)
		}
		if err := transaction.Commit(); err != nil {
			t.Fatal(err)
		}
	}
}

func normalReserveTimingObservations(jobID, shardID string, base time.Time, resource RemoteCIShardResources) []TimingObservation {
	compile := pureOverheadCompileGroupTimingObservation(jobID, shardID, base.Add(3*time.Second), base.Add(4*time.Second+500*time.Millisecond))
	compile.CompileResourceClassID = resource.ClassID
	compile.CompileResourceCPU = resource.CPU
	compile.CompileResourceMemoryGiB = resource.MemoryGiB
	return []TimingObservation{
		pureOverheadTimingObservation(jobID, cicontract.TimingScopeShard, shardID, "", cicontract.TimingECIWait, base, base.Add(time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation(jobID, cicontract.TimingScopeShard, shardID, "", cicontract.TimingSourceMaterialize, base.Add(time.Second), base.Add(2*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation(jobID, cicontract.TimingScopeShard, shardID, "", cicontract.TimingCandidateCompile, base.Add(2*time.Second), base.Add(3*time.Second), cicontract.TimingAggregationRaw),
		compile,
		pureOverheadTimingObservation(jobID, cicontract.TimingScopeWorkload, shardID, "workload-1", cicontract.TimingTotal, base.Add(4*time.Second), base.Add(7*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation(jobID, cicontract.TimingScopeWorkload, shardID, "workload-2", cicontract.TimingTotal, base.Add(6*time.Second+500*time.Millisecond), base.Add(8*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation(jobID, cicontract.TimingScopeShard, shardID, "", cicontract.TimingTotal, base, base.Add(10*time.Second), cicontract.TimingAggregationCriticalPath),
	}
}
