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

// TestNormalPlanningBootstrapsMissingResourceTierReserve locks the explicit
// isolated bootstrap path and rejects non-canonical resolution states.
func TestNormalPlanningBootstrapsMissingResourceTierReserve(t *testing.T) {
	context, resolved := resolveBootstrapPlanningContext(t)
	workloads := assertBootstrapOrdinaryPlanning(t, resolved)
	assertPartialReservePlanning(t, context, workloads)
	assertBootstrapCompilePlanning(t, resolved, workloads)
	assertInvalidReserveResolutions(t, context, resolved)
}

func resolveBootstrapPlanningContext(t *testing.T) (PlanningContext, PlanningContext) {
	t.Helper()
	context := testLinuxPlanningContext()
	context.ResourceTierReserves = nil
	resolved, err := ResolvePlanningContext(context, testPlanningLedger(context, nil))
	if err != nil {
		t.Fatalf("ResolvePlanningContext() error = %v", err)
	}
	if len(resolved.ResourceTierReserves) != int(cicontract.WorkloadResourceTierSlow) {
		t.Fatalf("bootstrap resolutions = %#v", resolved.ResourceTierReserves)
	}
	for _, reserve := range resolved.ResourceTierReserves {
		if reserve.Resolution != cicontract.WorkloadResourceTierReserveResolutionBootstrap {
			t.Fatalf("bootstrap reserve resolution = %#v", reserve)
		}
		if reserve.Measurement != nil {
			t.Fatalf("bootstrap reserve = %#v", reserve)
		}
	}
	return context, resolved
}

func assertBootstrapOrdinaryPlanning(t *testing.T, resolved PlanningContext) []PlannedWorkload {
	t.Helper()
	workloads := []PlannedWorkload{
		{Workload: Workload{ID: "bootstrap-a"}, EstimatedDurationMS: 1_000, ResourceCPU: 2, ResourceMemoryGiB: 4},
		{Workload: Workload{ID: "bootstrap-b"}, EstimatedDurationMS: 2_000, ResourceCPU: 2, ResourceMemoryGiB: 4},
	}
	shards, err := distributeTieredLPTWithinTarget(workloads, resolved)
	if err != nil {
		t.Fatalf("distributeTieredLPTWithinTarget() error = %v", err)
	}
	if len(shards) != len(workloads) {
		t.Fatalf("bootstrap ordinary shards = %d, want %d", len(shards), len(workloads))
	}
	evidence, err := deriveWorkloadPackingEvidence(shards, nil, resolved)
	if err != nil {
		t.Fatalf("deriveWorkloadPackingEvidence() error = %v", err)
	}
	assertBootstrapPackingEvidence(t, evidence)
	return workloads
}

func assertBootstrapPackingEvidence(t *testing.T, evidence []WorkloadPackingEvidence) {
	t.Helper()
	if len(evidence) != 1 {
		t.Fatalf("bootstrap packing evidence = %#v", evidence)
	}
	entry := evidence[0]
	if entry.Resolution != cicontract.WorkloadResourceTierReserveResolutionBootstrap || entry.SolverMode != cicontract.WorkloadPlanningReserveBootstrapSolverModeID {
		t.Fatalf("bootstrap packing identity = %#v", entry)
	}
	if entry.LowerBoundShards != 2 || entry.PlannedShards != 2 || entry.HeuristicGapShards != 0 {
		t.Fatalf("bootstrap packing proof = %#v", entry)
	}
}

func assertPartialReservePlanning(t *testing.T, context PlanningContext, workloads []PlannedWorkload) {
	t.Helper()
	partial := context
	partial.ResourceTierReserves = []cicontract.WorkloadResourceTierReserve{testMeasuredResourceTierReserve(cicontract.WorkloadResourceTierFast, "e")}
	partial, err := ResolvePlanningContext(partial, testPlanningLedger(context, nil))
	if err != nil {
		t.Fatalf("resolve partial reserve context: %v", err)
	}
	partialWorkloads := append(append([]PlannedWorkload(nil), workloads...),
		PlannedWorkload{Workload: Workload{ID: "bootstrap-medium"}, EstimatedDurationMS: 6_000, ResourceCPU: 4, ResourceMemoryGiB: 8})
	partialShards, err := distributeTieredLPTWithinTarget(partialWorkloads, partial)
	if err != nil {
		t.Fatalf("plan partial reserve tiers: %v", err)
	}
	if len(partialShards) != 2 {
		t.Fatalf("partial reserve shards = %#v, want one measured plus one bootstrap shard", partialShards)
	}
}

func assertBootstrapCompilePlanning(t *testing.T, resolved PlanningContext, workloads []PlannedWorkload) {
	t.Helper()
	groups := []CompileGroup{{GroupID: "bootstrap-group-a"}, {GroupID: "bootstrap-group-b"}}
	units := []compilePlanningUnit{
		{workloads: workloads[:1], group: &groups[0], costMS: 1_000, affinityKey: "a", sortID: "a", tier: cicontract.WorkloadResourceTierFast},
		{workloads: workloads[1:], group: &groups[1], costMS: 2_000, affinityKey: "b", sortID: "b", tier: cicontract.WorkloadResourceTierFast},
	}
	compileShards, err := distributeCompileUnitsForPlanningContext(units, resolved)
	if err != nil {
		t.Fatalf("distributeCompileUnitsForPlanningContext() error = %v", err)
	}
	if len(compileShards) != len(units) {
		t.Fatalf("bootstrap compile shards = %d, want %d", len(compileShards), len(units))
	}
}

func assertInvalidReserveResolutions(t *testing.T, context, resolved PlanningContext) {
	t.Helper()
	for _, resolution := range []cicontract.WorkloadResourceTierReserveResolution{"", "unknown"} {
		invalid := resolved
		invalid.ResourceTierReserves = append([]cicontract.WorkloadResourceTierReserve(nil), resolved.ResourceTierReserves...)
		invalid.ResourceTierReserves[0].Resolution = resolution
		if _, err := ResolvePlanningContext(invalid, testPlanningLedger(context, nil)); err == nil || !strings.Contains(err.Error(), "resolution") {
			t.Fatalf("resolution %q was not rejected first: %v", resolution, err)
		}
	}
}

func TestDeriveNormalResourceTierReservesExcludesAccountedIntervalUnion(t *testing.T) {
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
		assertMeasuredResourceTierReserve(t, index, reserve, wantTier)
	}
}

func assertMeasuredResourceTierReserve(t *testing.T, index int, reserve cicontract.WorkloadResourceTierReserve, wantTier cicontract.WorkloadResourceTier) {
	t.Helper()
	if reserve.ResourceTier != wantTier || reserve.Resolution != cicontract.WorkloadResourceTierReserveResolutionMeasured || reserve.Measurement == nil {
		t.Fatalf("reserve[%d] = %#v, want measured tier %d", index, reserve, wantTier)
	}
	if reserve.Measurement.P95MS != 2_000 || reserve.Measurement.SampleCount != 1 || reserve.Measurement.ProvenanceDigest == "" {
		t.Fatalf("reserve[%d] measurement = %#v, want P95 2000ms", index, reserve.Measurement)
	}
}

func TestLoadPlanningDerivesNormalResourceTierReservesFromSQLiteTiming(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := normalReserveTestOverhead(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	seedSQLiteNormalReserveEvidenceWithoutCompile(t, store, time.UnixMilli(10_000).UTC())
	seedSQLiteNormalReserveExecutionIdentities(t, store, overhead.Platform, overhead.Runner, overhead.Toolchain, DurationExecutionModeNormal)
	database := openWorkloadPassDatabase(t, store)
	insertRawDurationSample(t, database, "1", "planning-read-row", "small", overhead.Platform, overhead.Runner, overhead.Toolchain)
	execLocalAuthorityRowsAffectedOne(t, database, "duration metadata advance", `UPDATE duration_ledger_meta SET generation = ? WHERE singleton = 1`, 4)
	database.Close()
	context := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	snapshot, err := store.LoadPlanning(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ResourceTierReserves) != 3 || snapshot.SampleIndex == nil {
		t.Fatalf("SQLite planning reserves/index = %#v/%v", snapshot.ResourceTierReserves, snapshot.SampleIndex)
	}
	target, err := snapshot.SampleIndex.context.EffectiveTargetDurationForResourceTier(cicontract.WorkloadResourceTierMedium)
	if err != nil || target != 97_000 {
		t.Fatalf("SQLite planning medium target = %d, %v; want 97000 without compile-group timing", target, err)
	}
}

func normalReserveTestOverhead(digest string) ShardOrchestrationOverhead {
	overhead := durationLedgerSQLiteShardOverheadRoundTripValue(digest)
	overhead.Runner = "sha256:" + strings.Repeat("d", 64)
	overhead.Toolchain = "sha256:" + strings.Repeat("e", 64)
	return overhead
}

func TestLoadPlanningRejectsNormalReserveFromDifferentRunnerToolchain(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := normalReserveTestOverhead(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	base := time.UnixMilli(10_000).UTC()
	seedSQLiteNormalReserveEvidence(t, store, base)
	seedSQLiteNormalReserveExecutionIdentities(t, store, overhead.Platform,
		"sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64), DurationExecutionModeNormal)
	context := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	snapshot, err := store.LoadPlanning(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ResourceTierReserves) != 0 || snapshot.SampleIndex == nil || len(snapshot.SampleIndex.context.ResourceTierReserves) != int(cicontract.WorkloadResourceTierSlow) {
		t.Fatalf("normal planning accepted mismatched execution identity: reserves=%#v context=%#v", snapshot.ResourceTierReserves, snapshot.SampleIndex)
	}
}

func TestLoadPlanningUsesOnlyExactV19SideRows(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := normalReserveTestOverhead(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	seedSQLiteNormalReserveEvidence(t, store, time.UnixMilli(10_000).UTC())
	seedSQLiteNormalReserveExecutionIdentities(t, store, overhead.Platform, overhead.Runner, overhead.Toolchain, DurationExecutionModeNormal)
	deleteSQLiteNormalReserveExecutionIdentity(t, store, "reserve-job-c")
	updateSQLiteNormalReserveExecutionIdentity(t, store, "reserve-job-b", overhead.Platform, "sha256:"+strings.Repeat("f", 64), overhead.Toolchain, DurationExecutionModeNormal)
	context := PlanningContext{Platform: overhead.Platform, Runner: overhead.Runner, Toolchain: overhead.Toolchain, TargetDurationMS: FullCITargetDurationMS, AcceptedSnapshotID: overhead.AcceptedSnapshotID}
	snapshot, err := store.LoadPlanning(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ResourceTierReserves) != 1 || snapshot.ResourceTierReserves[0].ResourceTier != cicontract.WorkloadResourceTierFast {
		t.Fatalf("mixed/missing v19 side rows = %#v, want exact fast tier only", snapshot.ResourceTierReserves)
	}
}

func TestLoadPlanningUsesV19ExecutionIdentityDespiteMixedCompileIdentity(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := normalReserveTestOverhead(sample.ProvenanceDigest)
	durationLedgerSQLiteRecordShardOverheadRoundTrip(t, store, sample, overhead)
	base := time.UnixMilli(10_000).UTC()
	seedSQLiteNormalReserveEvidence(t, store, base)
	seedSQLiteNormalReserveExecutionIdentities(t, store, overhead.Platform, overhead.Runner, overhead.Toolchain, DurationExecutionModeNormal)
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
	snapshot, err := store.LoadPlanning(context)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.ResourceTierReserves) != 3 {
		t.Fatalf("normal planning reserves = %#v, want v19 side-row measured reserves", snapshot.ResourceTierReserves)
	}
}

func TestLoadSQLiteNormalResourceTierReservesUsesOneSetBasedQuery(t *testing.T) {
	store := newTestDurationLedgerStore(t)
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	seedAcceptedGenerationForTest(t, store, 1)
	sample := durationLedgerSQLiteShardOverheadRoundTripSample()
	overhead := normalReserveTestOverhead(sample.ProvenanceDigest)
	base := time.UnixMilli(10_000).UTC()
	seedSQLiteNormalReserveEvidence(t, store, base)
	seedSQLiteNormalReserveExecutionIdentities(t, store, overhead.Platform, overhead.Runner, overhead.Toolchain, DurationExecutionModeNormal)

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
		overhead.CalibrationResourceClassID)
	assertSQLiteQueryPlanAccess(t, details, []string{
		"idx_ci_runs_accepted_generation", "sqlite_autoindex_ci_remote_run_execution_identities_1",
		"idx_ci_timing_observations_compile_group", "sqlite_autoindex_ci_shards_1",
	})
	assertSQLiteQueryPlanNoFullTableScan(t, details, []string{"runs", "run_identity", "shards", "timing"})
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

func seedSQLiteNormalReserveExecutionIdentities(t *testing.T, store *DurationLedgerStore, platform, runner, toolchain, mode string) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	for index := range 3 {
		jobID := "reserve-job-" + string(rune('a'+index))
		if _, err := database.Exec(`INSERT INTO ci_remote_run_execution_identities (job_id, platform, runner_identity_digest, toolchain_digest, execution_mode) VALUES (?, ?, ?, ?, ?)`, jobID, platform, runner, toolchain, mode); err != nil {
			t.Fatal(err)
		}
	}
}

func updateSQLiteNormalReserveExecutionIdentity(t *testing.T, store *DurationLedgerStore, jobID, platform, runner, toolchain, mode string) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`UPDATE ci_remote_run_execution_identities SET platform = ?, runner_identity_digest = ?, toolchain_digest = ?, execution_mode = ? WHERE job_id = ?`, platform, runner, toolchain, mode, jobID); err != nil {
		t.Fatal(err)
	}
}

func deleteSQLiteNormalReserveExecutionIdentity(t *testing.T, store *DurationLedgerStore, jobID string) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if _, err := database.Exec(`DELETE FROM ci_remote_run_execution_identities WHERE job_id = ?`, jobID); err != nil {
		t.Fatal(err)
	}
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
	seedSQLiteNormalReserveEvidenceWithCompileTiming(t, store, base, true)
}

func seedSQLiteNormalReserveEvidenceWithoutCompile(t *testing.T, store *DurationLedgerStore, base time.Time) {
	seedSQLiteNormalReserveEvidenceWithCompileTiming(t, store, base, false)
}

func seedSQLiteNormalReserveEvidenceWithCompileTiming(t *testing.T, store *DurationLedgerStore, base time.Time, includeCompile bool) {
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
		record := RemoteCIRunRecord{JobID: jobID, Status: ResultStatusPassed, Authoritative: true, ExecutionMode: DurationExecutionModeNormal}
		shard := RemoteCIShardRecord{ShardIdentity: shardID, ContainerGroup: "eci-" + jobID, ContainerStatus: "Succeeded", MaterializationTiming: measuredShardMaterializationTiming(shardID), Resources: resource}
		if err := insertSQLiteRemoteCIRunShard(transaction, record, shard); err != nil {
			transaction.Rollback()
			t.Fatal(err)
		}
		observations := normalReserveTimingObservations(jobID, shardID, base, resource)
		if !includeCompile {
			observations = append(append([]TimingObservation(nil), observations[:3]...), observations[4:]...)
		}
		if err := replaceSQLiteTimingObservations(transaction, jobID, observations); err != nil {
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
