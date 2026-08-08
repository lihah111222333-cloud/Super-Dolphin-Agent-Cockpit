package gate

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/cicontract"
)

func TestTimingObservationRejectsNotMeasuredAndMissingNotApplicableReason(t *testing.T) {
	base := TimingObservation{JobID: "job-1", Scope: cicontract.TimingScopeRun, Phase: cicontract.TimingTotal, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewNotApplicableCacheEvidence("run_has_no_workload_cache")}
	base.Measurement = "not_measured"
	if err := base.Validate(); err == nil {
		t.Fatal("not_measured authority unexpectedly accepted")
	}
	base.Measurement = cicontract.ObservationNotApplicable
	if err := base.Validate(); err == nil {
		t.Fatal("unreasoned not_applicable authority unexpectedly accepted")
	}
}

func TestTimingObservationRejectsNonUTCTimestamps(t *testing.T) {
	base := time.Date(2026, time.January, 1, 0, 0, 0, 0, time.FixedZone("local", 9*60*60))
	observation := pureOverheadTimingObservation("job-local-time", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingECIWait, base, base.Add(time.Second), cicontract.TimingAggregationRaw)
	if err := observation.Validate(); err == nil {
		t.Fatal("non-UTC timing observation unexpectedly accepted")
	}
}

func TestDeriveShardOrchestrationOverheadUsesAccountedIntervalUnion(t *testing.T) {
	base := time.UnixMilli(1_000).UTC()
	observations := []TimingObservation{
		pureOverheadTimingObservation("job-union", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingECIWait, base, base.Add(1*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-union", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingSourceMaterialize, base.Add(1*time.Second), base.Add(2*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-union", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingCandidateCompile, base.Add(2*time.Second), base.Add(3*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-union", cicontract.TimingScopeWorkload, "shard-1", "workload-1", cicontract.TimingTotal, base.Add(4*time.Second), base.Add(7*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-union", cicontract.TimingScopeWorkload, "shard-1", "workload-2", cicontract.TimingTotal, base.Add(6*time.Second+500*time.Millisecond), base.Add(8*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadCompileGroupTimingObservation("job-union", "shard-1", base.Add(3*time.Second), base.Add(4*time.Second+500*time.Millisecond)),
		pureOverheadTimingObservation("job-union", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingTotal, base, base.Add(10*time.Second), cicontract.TimingAggregationCriticalPath),
	}
	p95, sampleCount, digest, samples, err := DeriveShardOrchestrationOverhead(observations)
	if err != nil {
		t.Fatal(err)
	}
	assertDerivedOverheadAggregate(t, p95, sampleCount, digest, samples)
	assertDerivedOverheadSample(t, base, digest, samples[0])
}

func assertDerivedOverheadAggregate(t *testing.T, p95 int64, sampleCount int, digest string, samples []ShardOrchestrationOverheadSample) {
	t.Helper()
	if p95 != 2_000 {
		t.Fatalf("derived overhead p95 = %d, want 2000", p95)
	}
	if sampleCount != 1 || len(samples) != 1 {
		t.Fatalf("derived overhead count/samples = %d/%d, want 1/1", sampleCount, len(samples))
	}
	if digest == "" {
		t.Fatal("derived overhead provenance digest is empty")
	}
}

func assertDerivedOverheadSample(t *testing.T, base time.Time, digest string, sample ShardOrchestrationOverheadSample) {
	t.Helper()
	if sample.AccountedDurationMS != 8_000 || sample.AccountedIntervalCount != 6 || sample.OverheadMS != 2_000 {
		t.Fatalf("accounted sample = %#v, want duration=8000 count=6 overhead=2000", sample)
	}
	if sample.WorkloadEnvelopeStart != base.Add(4*time.Second) || sample.WorkloadEnvelopeEnd != base.Add(8*time.Second) {
		t.Fatalf("workload envelope = %v..%v, want %v..%v", sample.WorkloadEnvelopeStart, sample.WorkloadEnvelopeEnd, base.Add(4*time.Second), base.Add(8*time.Second))
	}
	if sample.ProvenanceDigest != digest {
		t.Fatalf("provenance digest = %q/%q, want equality", sample.ProvenanceDigest, digest)
	}
}

func TestDeriveShardOrchestrationOverheadRejectsDuplicateAccountedInterval(t *testing.T) {
	base := time.UnixMilli(2_000).UTC()
	observations := []TimingObservation{
		pureOverheadTimingObservation("job-duplicate", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingECIWait, base, base.Add(time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-duplicate", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingSourceMaterialize, base.Add(time.Second), base.Add(2*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-duplicate", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingCandidateCompile, base.Add(2*time.Second), base.Add(3*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-duplicate", cicontract.TimingScopeWorkload, "shard-1", "workload-1", cicontract.TimingTotal, base.Add(3*time.Second), base.Add(4*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-duplicate", cicontract.TimingScopeWorkload, "shard-1", "workload-1", cicontract.TimingTotal, base.Add(3*time.Second), base.Add(4*time.Second), cicontract.TimingAggregationRaw),
		pureOverheadTimingObservation("job-duplicate", cicontract.TimingScopeShard, "shard-1", "", cicontract.TimingTotal, base, base.Add(5*time.Second), cicontract.TimingAggregationCriticalPath),
	}
	if _, _, _, _, err := DeriveShardOrchestrationOverhead(observations); err == nil {
		t.Fatal("duplicate workload total unexpectedly accepted")
	}
}

func pureOverheadTimingObservation(jobID string, scope cicontract.TimingScope, shard string, workload GateID, phase cicontract.TimingPhase, startedAt, completedAt time.Time, aggregation cicontract.TimingAggregation) TimingObservation {
	return TimingObservation{
		JobID: jobID, Scope: scope, ShardIdentity: shard, WorkloadID: workload, Phase: phase,
		StartedAt: startedAt, CompletedAt: completedAt, DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		Measurement: cicontract.ObservationMeasured, Aggregation: aggregation,
		CacheEvidence: NewNotApplicableCacheEvidence("overhead_derivation_test"),
	}
}

func pureOverheadCompileGroupTimingObservation(jobID, shard string, startedAt, completedAt time.Time) TimingObservation {
	digest := "sha256:" + strings.Repeat("a", 64)
	return TimingObservation{
		JobID: jobID, Scope: cicontract.TimingScopeCompileGroup, ShardIdentity: shard, Phase: cicontract.TimingTestBinaryCompile,
		StartedAt: startedAt, CompletedAt: completedAt, DurationMS: completedAt.Sub(startedAt).Milliseconds(),
		Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw,
		CacheEvidence:  NewCompileGroupCacheEvidence(CompileGroupExecution{CacheMisses: 1}),
		CompileGroupID: digest, CompileArtifactKey: "sha256:" + strings.Repeat("b", 64), CompilePackageTarget: "./internal/devtools/gate",
		CompileWorkloadIDs: []GateID{"workload-1", "workload-2"}, CompileArtifactSHA256: "sha256:" + strings.Repeat("c", 64), CompileArtifactSize: 1,
		CompileCacheMisses: 1, CompileCacheStatus: string(CacheObservationMiss), CompileStatus: string(ResultStatusPassed), CompileExitCode: 0,
		CompileCommandDigest: digest, CompileProfileDigest: "sha256:" + strings.Repeat("d", 64), CompileResourceClassID: "medium",
		CompileResourceCPU: 4, CompileResourceMemoryGiB: 8, CompileExecutionMode: DurationExecutionModeNormal,
	}
}

// TestTimingObservationSQLiteRoundTrip 保证 SQLite 仅保存并回读严格结构化 cache evidence。
func TestTimingObservationSQLiteRoundTrip(t *testing.T) {
	store, err := NewDurationLedgerStore(t.TempDir() + "/duration-ledger.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	transaction, err := database.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer transaction.Rollback()
	startedAt := time.UnixMilli(100).UTC()
	if _, err := transaction.Exec(`INSERT INTO ci_runs (job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text) VALUES (?, 0, ?, ?, ?, ?, '1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "roundtrip", "manual_cli", "local_fast", "sha256:plan", "sha256:catalog", "snapshot-1", strings.Repeat("a", 40), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64), "runner", "failed", 0, startedAt.UnixMilli(), startedAt.Add(time.Millisecond).UnixMilli(), 1, ""); err != nil {
		t.Fatal(err)
	}
	putProfile := ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationPut, CacheMeasurement: "measured", CachePutCount: 1}
	want := TimingObservation{JobID: "roundtrip", Scope: cicontract.TimingScopeWorkload, ShardIdentity: "shard-1", WorkloadID: "workload-1", Phase: cicontract.TimingTotal, StartedAt: startedAt, CompletedAt: startedAt.Add(time.Millisecond), DurationMS: 1, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewTimingCacheEvidenceFromProfile(putProfile)}
	if err := replaceSQLiteTimingObservations(transaction, "roundtrip", []TimingObservation{want}); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := loadTimingObservations(database, "roundtrip")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []TimingObservation{want}) {
		t.Fatalf("stored timing observations = %#v, want %#v", got, []TimingObservation{want})
	}
}

func TestTimingObservationSQLiteRoundTripPreservesZeroNotApplicableTimes(t *testing.T) {
	store := newTimingObservationStore(t)
	database, transaction := beginTimingObservationTransaction(t, store)
	defer database.Close()
	if _, err := transaction.Exec(`INSERT INTO ci_runs (job_id, force, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, image_cache_snapshot_id, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text) VALUES (?, 0, ?, ?, ?, ?, '1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "not-applicable-roundtrip", "manual_cli", "local_fast", "sha256:plan", "sha256:catalog", "snapshot-1", strings.Repeat("a", 40), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64), "runner", "failed", 0, 100, 101, 1, ""); err != nil {
		t.Fatal(err)
	}
	observation := TimingObservation{
		JobID: "not-applicable-roundtrip", Scope: cicontract.TimingScopeWorkload,
		ShardIdentity: "shard-1", WorkloadID: "workload-1", Phase: cicontract.TimingECIWait,
		Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:shard-1",
		Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewNotApplicableCacheEvidence("shard_scoped"),
	}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := replaceSQLiteTimingObservations(transaction, observation.JobID, []TimingObservation{observation}); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err := loadTimingObservations(database, observation.JobID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []TimingObservation{observation}) {
		t.Fatalf("stored not_applicable timing observation = %#v, want %#v", got, []TimingObservation{observation})
	}
}

func newTimingObservationStore(t *testing.T) *DurationLedgerStore {
	t.Helper()
	store, err := NewDurationLedgerStore(t.TempDir() + "/duration-ledger.sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CompareAndSwap(0, NewDurationLedger()); err != nil {
		t.Fatal(err)
	}
	return store
}

func beginTimingObservationTransaction(t *testing.T, store *DurationLedgerStore) (*sql.DB, *sql.Tx) {
	t.Helper()
	database, err := store.openSQLiteAuthority(false)
	if err != nil {
		t.Fatal(err)
	}
	transaction, err := database.Begin()
	if err != nil {
		database.Close()
		t.Fatal(err)
	}
	return database, transaction
}

// TestTimingCacheEvidenceMeasuredStatuses 保证 profile 与 timing evidence 共享严格状态语义。
func TestTimingCacheEvidenceMeasuredStatuses(t *testing.T) {
	testCases := []struct {
		name    string
		profile ExecutionProfile
	}{
		{name: "hit", profile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationHit, CacheMeasurement: "measured", PrivateHitCount: 1}},
		{name: "miss", profile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationMiss, CacheMeasurement: "measured", CacheMissCount: 1}},
		{name: "put", profile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationPut, CacheMeasurement: "measured", CachePutCount: 1}},
		{name: "zero lookup", profile: ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured"}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			if err := testCase.profile.Validate(); err != nil {
				t.Fatalf("profile validation: %v", err)
			}
			if err := NewTimingCacheEvidenceFromProfile(testCase.profile).Validate(); err != nil {
				t.Fatalf("timing cache evidence validation: %v", err)
			}
		})
	}
	invalidPut := ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationStatus("other"), CacheMeasurement: "measured"}
	if err := invalidPut.Validate(); err == nil {
		t.Fatal("unknown cache status was accepted")
	}
	invalidZeroLookup := ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: CacheObservationNotApplicable, CacheMeasurement: "measured", CacheMissCount: 1}
	if err := invalidZeroLookup.Validate(); err == nil {
		t.Fatal("zero-lookup cache status with observations was accepted")
	}
}

// TestValidateAuthoritativeTimingObservationsRejectsCoverageAndBindingDrift 覆盖权威账本的缺相位、额外 workload、job/shard 及缓存计数漂移。
func TestValidateAuthoritativeTimingObservationsRejectsCoverageAndBindingDrift(t *testing.T) {
	profile := ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "hit", CacheMeasurement: "measured", PrivateHitCount: 2, BaselineHitCount: 3, CacheMissCount: 5, CachePutCount: 7, StartupMS: 1, TestBodyMS: 6, TotalMS: 7}
	execution := PlanGateExecution{ShardIdentity: "shard-1", GateID: "workload-1", StartedAt: time.UnixMilli(103).UTC(), CompletedAt: time.UnixMilli(110).UTC(), ExecutionProfile: profile}
	shards := []RemoteCIShardRecord{{ShardIdentity: "shard-1", Workloads: []GateID{"workload-1"}}}
	observations := authoritativeTimingObservationsForTest("job-1", execution)
	if err := ValidateAuthoritativeTimingObservations("job-1", observations, []PlanGateExecution{execution}, shards); err != nil {
		t.Fatal(err)
	}
	assertAuthoritativeTimingDriftCases(t, profile, execution, observations, shards)
}

type timingObservationDriftCase struct {
	name string
	edit func([]TimingObservation, *PlanGateExecution, ExecutionProfile) []TimingObservation
	want string
}

func assertAuthoritativeTimingDriftCases(t *testing.T, profile ExecutionProfile, execution PlanGateExecution, observations []TimingObservation, shards []RemoteCIShardRecord) {
	t.Helper()
	for _, testCase := range authoritativeTimingDriftCases() {
		t.Run(testCase.name, func(t *testing.T) {
			copyObservations := append([]TimingObservation(nil), observations...)
			copyExecution := execution
			edited := testCase.edit(copyObservations, &copyExecution, profile)
			err := ValidateAuthoritativeTimingObservations("job-1", edited, []PlanGateExecution{copyExecution}, shards)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func authoritativeTimingDriftCases() []timingObservationDriftCase {
	return []timingObservationDriftCase{
		{name: "missing phase", edit: dropTimingObservation, want: "missing"},
		{name: "extra workload", edit: addExtraTimingWorkload, want: "extra"},
		{name: "wrong job", edit: changeTimingJob, want: "job binding"},
		{name: "wrong shard", edit: changeTimingShard, want: "shard binding"},
		{name: "cache count", edit: changeTimingCacheCount, want: "cache evidence"},
		{name: "compile overlaps execution", edit: changeTimingCompileEnd, want: "compile overlaps execution"},
		{name: "eci wait overlaps source", edit: changeTimingECIWaitEnd, want: "ECI wait overlaps source materialization"},
		{name: "shard total omits phase", edit: changeTimingShardTotalEnd, want: "total does not contain phase"},
		{name: "run does not contain shard", edit: changeTimingRunStart, want: "does not contain shard"},
		{name: "workload startup overlaps body", edit: changeTimingWorkloadStartupEnd, want: "does not equal the exact workload union"},
	}
}

func dropTimingObservation(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	return observations[1:]
}

func addExtraTimingWorkload(observations []TimingObservation, _ *PlanGateExecution, profile ExecutionProfile) []TimingObservation {
	extra := observations[len(observations)-1]
	extra.Scope, extra.ShardIdentity, extra.WorkloadID, extra.Phase = cicontract.TimingScopeWorkload, "shard-1", "extra", cicontract.TimingTotal
	extra.CacheEvidence = NewTimingCacheEvidenceFromProfile(profile)
	return append(observations, extra)
}

func changeTimingJob(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	observations[0].JobID = "other"
	return observations
}

func changeTimingShard(observations []TimingObservation, execution *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	execution.ShardIdentity = "other"
	return observations
}

func changeTimingCacheCount(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	for index := range observations {
		if observations[index].Scope == cicontract.TimingScopeWorkload && observations[index].Phase == cicontract.TimingStartup {
			observations[index].CacheEvidence.Go.PrivateHits++
		}
	}
	return observations
}

func changeTimingCompileEnd(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	return changeTimingPhaseEnd(observations, cicontract.TimingScopeShard, cicontract.TimingCandidateCompile, time.UnixMilli(105).UTC(), 3)
}

func changeTimingECIWaitEnd(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	return changeTimingPhaseEnd(observations, cicontract.TimingScopeShard, cicontract.TimingECIWait, time.UnixMilli(102).UTC(), 2)
}

func changeTimingShardTotalEnd(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	return changeTimingPhaseEnd(observations, cicontract.TimingScopeShard, cicontract.TimingTotal, time.UnixMilli(109).UTC(), 9)
}

func changeTimingRunStart(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	for index := range observations {
		if observations[index].Scope == cicontract.TimingScopeRun {
			observations[index].StartedAt = time.UnixMilli(101).UTC()
			observations[index].DurationMS = 11
		}
	}
	return observations
}

func changeTimingWorkloadStartupEnd(observations []TimingObservation, _ *PlanGateExecution, _ ExecutionProfile) []TimingObservation {
	return changeTimingPhaseEnd(observations, cicontract.TimingScopeWorkload, cicontract.TimingStartup, time.UnixMilli(105).UTC(), 2)
}

func changeTimingPhaseEnd(observations []TimingObservation, scope cicontract.TimingScope, phase cicontract.TimingPhase, completedAt time.Time, durationMS int64) []TimingObservation {
	for index := range observations {
		if observations[index].Scope == scope && observations[index].Phase == phase {
			observations[index].CompletedAt = completedAt
			observations[index].DurationMS = durationMS
		}
	}
	return observations
}

func TestTimingObservationDurationSemantics(t *testing.T) {
	started := time.UnixMilli(100).UTC()
	base := TimingObservation{JobID: "job-1", Scope: cicontract.TimingScopeRun, Phase: cicontract.TimingTotal, StartedAt: started, CompletedAt: started.Add(10 * time.Millisecond), DurationMS: 10, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewNotApplicableCacheEvidence("run_has_no_workload_cache")}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	base.DurationMS = 9
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "must equal") {
		t.Fatalf("raw duration drift error = %v", err)
	}
	base.Aggregation, base.DurationMS = cicontract.TimingAggregationIntervalUnion, 11
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("interval union envelope overflow error = %v", err)
	}
	base.Measurement, base.DurationMS, base.StartedAt, base.CompletedAt, base.Reason = cicontract.ObservationNotApplicable, 1, time.Time{}, time.Time{}, "not_used"
	if err := base.Validate(); err == nil || !strings.Contains(err.Error(), "needs only") {
		t.Fatalf("not applicable nonzero duration error = %v", err)
	}
}

func authoritativeTimingObservationsForTest(jobID string, execution PlanGateExecution) []TimingObservation {
	measured := func(scope cicontract.TimingScope, shard string, workload GateID, phase cicontract.TimingPhase, startedAt, completedAt time.Time, evidence CacheEvidence) TimingObservation {
		return TimingObservation{JobID: jobID, Scope: scope, ShardIdentity: shard, WorkloadID: workload, Phase: phase, StartedAt: startedAt, CompletedAt: completedAt, DurationMS: completedAt.Sub(startedAt).Milliseconds(), Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: evidence}
	}
	notApplicable := func(phase cicontract.TimingPhase) TimingObservation {
		return TimingObservation{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: execution.ShardIdentity, WorkloadID: execution.GateID, Phase: phase, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + execution.ShardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)}
	}
	startedAt := execution.StartedAt.UTC().Truncate(time.Millisecond)
	completedAt := execution.CompletedAt.UTC().Truncate(time.Millisecond)
	workloadStartupCompletedAt := startedAt.Add(time.Duration(execution.ExecutionProfile.StartupMS) * time.Millisecond)
	workloadTestBodyStartedAt := completedAt.Add(-time.Duration(execution.ExecutionProfile.TestBodyMS) * time.Millisecond)
	shardStartedAt := startedAt.Add(-3 * time.Millisecond)
	shardCompletedAt := completedAt.Add(time.Millisecond)
	runCompletedAt := completedAt.Add(2 * time.Millisecond)
	runTotal := measured(cicontract.TimingScopeRun, "", "", cicontract.TimingTotal, shardStartedAt, runCompletedAt, NewNotApplicableCacheEvidence("run_has_no_workload_cache"))
	runTotal.Aggregation = cicontract.TimingAggregationCriticalPath
	observations := []TimingObservation{runTotal}
	shardIntervals := map[cicontract.TimingPhase][2]time.Time{
		cicontract.TimingECIWait:           {shardStartedAt, shardStartedAt.Add(time.Millisecond)},
		cicontract.TimingSourceMaterialize: {shardStartedAt.Add(time.Millisecond), shardStartedAt.Add(2 * time.Millisecond)},
		cicontract.TimingCandidateCompile:  {shardStartedAt.Add(2 * time.Millisecond), startedAt},
		cicontract.TimingStartup:           {startedAt, workloadStartupCompletedAt},
		cicontract.TimingTestBody:          {workloadTestBodyStartedAt, completedAt},
		cicontract.TimingTotal:             {shardStartedAt, shardCompletedAt},
	}
	for _, phase := range cicontract.TimingPhases() {
		interval := shardIntervals[phase]
		shardObservation := measured(cicontract.TimingScopeShard, execution.ShardIdentity, "", phase, interval[0], interval[1], NewNotApplicableCacheEvidence("shard_has_no_workload_cache"))
		switch phase {
		case cicontract.TimingStartup, cicontract.TimingTestBody:
			shardObservation.Aggregation = cicontract.TimingAggregationIntervalUnion
		case cicontract.TimingTotal:
			shardObservation.Aggregation = cicontract.TimingAggregationCriticalPath
		}
		observations = append(observations, shardObservation)
		if phase == cicontract.TimingECIWait || phase == cicontract.TimingSourceMaterialize || phase == cicontract.TimingCandidateCompile {
			observations = append(observations, notApplicable(phase))
		}
	}
	workloadIntervals := map[cicontract.TimingPhase][2]time.Time{
		cicontract.TimingStartup:  {startedAt, workloadStartupCompletedAt},
		cicontract.TimingTestBody: {workloadTestBodyStartedAt, completedAt},
		cicontract.TimingTotal:    {startedAt, completedAt},
	}
	for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody, cicontract.TimingTotal} {
		interval := workloadIntervals[phase]
		observations = append(observations, measured(cicontract.TimingScopeWorkload, execution.ShardIdentity, execution.GateID, phase, interval[0], interval[1], NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)))
	}
	return observations
}

func TestTimingObservationMeasuredInterval(t *testing.T) {
	started := time.UnixMilli(100).UTC()
	observation := TimingObservation{JobID: "job-1", Scope: cicontract.TimingScopeRun, Phase: cicontract.TimingTotal, StartedAt: started, CompletedAt: started.Add(time.Millisecond), DurationMS: 1, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationCriticalPath, CacheEvidence: NewNotApplicableCacheEvidence("run_has_no_workload_cache")}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
}
