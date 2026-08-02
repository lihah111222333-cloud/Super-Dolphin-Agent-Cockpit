package gate

import (
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
	if _, err := transaction.Exec(`INSERT INTO ci_runs (job_id, entrypoint, profile, plan_digest, catalog_digest, accepted_generation, source_tree_sha, candidate_gate_source_sha256, candidate_gate_toolchain_sha256, runner_image, status, authoritative, started_at_unix_ms, completed_at_unix_ms, cleanup_complete, error_text) VALUES (?, ?, ?, ?, ?, '1', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, "roundtrip", "manual_cli", "local_fast", "sha256:plan", "sha256:catalog", strings.Repeat("a", 40), "sha256:"+strings.Repeat("b", 64), "sha256:"+strings.Repeat("c", 64), "runner", "failed", 0, startedAt.UnixMilli(), startedAt.Add(time.Millisecond).UnixMilli(), 1, ""); err != nil {
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
	profile := ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: "hit", CacheMeasurement: "measured", PrivateHitCount: 2, BaselineHitCount: 3, CacheMissCount: 5, CachePutCount: 7}
	execution := PlanGateExecution{ShardIdentity: "shard-1", GateID: "workload-1", ExecutionProfile: profile}
	shards := []RemoteCIShardRecord{{ShardIdentity: "shard-1", Workloads: []GateID{"workload-1"}}}
	observations := authoritativeTimingObservationsForTest("job-1", execution)
	if err := ValidateAuthoritativeTimingObservations("job-1", observations, []PlanGateExecution{execution}, shards); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		edit func([]TimingObservation, *PlanGateExecution) []TimingObservation
		want string
	}{
		{name: "missing phase", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			return observations[1:]
		}, want: "missing"},
		{name: "extra workload", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			extra := observations[len(observations)-1]
			extra.Scope, extra.ShardIdentity, extra.WorkloadID, extra.Phase = cicontract.TimingScopeWorkload, "shard-1", "extra", cicontract.TimingTotal
			extra.CacheEvidence = NewTimingCacheEvidenceFromProfile(profile)
			return append(observations, extra)
		}, want: "extra"},
		{name: "wrong job", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			observations[0].JobID = "other"
			return observations
		}, want: "job binding"},
		{name: "wrong shard", edit: func(observations []TimingObservation, execution *PlanGateExecution) []TimingObservation {
			execution.ShardIdentity = "other"
			return observations
		}, want: "shard binding"},
		{name: "cache count", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			for index := range observations {
				if observations[index].Scope == cicontract.TimingScopeWorkload && observations[index].Phase == cicontract.TimingStartup {
					observations[index].CacheEvidence.Go.PrivateHits++
				}
			}
			return observations
		}, want: "cache evidence"},
		{name: "compile overlaps execution", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			for index := range observations {
				if observations[index].Scope == cicontract.TimingScopeShard && observations[index].Phase == cicontract.TimingCandidateCompile {
					observations[index].CompletedAt = time.UnixMilli(105)
					observations[index].DurationMS = 3
				}
			}
			return observations
		}, want: "compile overlaps execution"},
		{name: "eci wait overlaps source", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			for index := range observations {
				if observations[index].Scope == cicontract.TimingScopeShard && observations[index].Phase == cicontract.TimingECIWait {
					observations[index].CompletedAt = time.UnixMilli(102)
					observations[index].DurationMS = 2
				}
			}
			return observations
		}, want: "ECI wait overlaps source materialization"},
		{name: "shard total omits phase", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			for index := range observations {
				if observations[index].Scope == cicontract.TimingScopeShard && observations[index].Phase == cicontract.TimingTotal {
					observations[index].CompletedAt = time.UnixMilli(109)
					observations[index].DurationMS = 9
				}
			}
			return observations
		}, want: "total does not contain phase"},
		{name: "run does not contain shard", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			for index := range observations {
				if observations[index].Scope == cicontract.TimingScopeRun {
					observations[index].StartedAt = time.UnixMilli(101)
					observations[index].DurationMS = 11
				}
			}
			return observations
		}, want: "does not contain shard"},
		{name: "workload startup overlaps body", edit: func(observations []TimingObservation, _ *PlanGateExecution) []TimingObservation {
			for index := range observations {
				if observations[index].Scope == cicontract.TimingScopeWorkload && observations[index].Phase == cicontract.TimingStartup {
					observations[index].CompletedAt = time.UnixMilli(105)
					observations[index].DurationMS = 2
				}
			}
			return observations
		}, want: "does not equal the exact workload union"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			copyObservations := append([]TimingObservation(nil), observations...)
			copyExecution := execution
			edited := testCase.edit(copyObservations, &copyExecution)
			err := ValidateAuthoritativeTimingObservations("job-1", edited, []PlanGateExecution{copyExecution}, shards)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}
}

func TestTimingObservationDurationSemantics(t *testing.T) {
	started := time.UnixMilli(100)
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
	measured := func(scope cicontract.TimingScope, shard string, workload GateID, phase cicontract.TimingPhase, startedMS, completedMS int64, evidence CacheEvidence) TimingObservation {
		return TimingObservation{JobID: jobID, Scope: scope, ShardIdentity: shard, WorkloadID: workload, Phase: phase, StartedAt: time.UnixMilli(startedMS), CompletedAt: time.UnixMilli(completedMS), DurationMS: completedMS - startedMS, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: evidence}
	}
	notApplicable := func(phase cicontract.TimingPhase) TimingObservation {
		return TimingObservation{JobID: jobID, Scope: cicontract.TimingScopeWorkload, ShardIdentity: execution.ShardIdentity, WorkloadID: execution.GateID, Phase: phase, Measurement: cicontract.ObservationNotApplicable, Reason: "shard_scoped:" + execution.ShardIdentity, Aggregation: cicontract.TimingAggregationRaw, CacheEvidence: NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)}
	}
	runTotal := measured(cicontract.TimingScopeRun, "", "", cicontract.TimingTotal, 100, 112, NewNotApplicableCacheEvidence("run_has_no_workload_cache"))
	runTotal.Aggregation = cicontract.TimingAggregationCriticalPath
	observations := []TimingObservation{runTotal}
	shardIntervals := map[cicontract.TimingPhase][2]int64{
		cicontract.TimingECIWait:           {100, 101},
		cicontract.TimingSourceMaterialize: {101, 102},
		cicontract.TimingCandidateCompile:  {102, 103},
		cicontract.TimingStartup:           {103, 104},
		cicontract.TimingTestBody:          {104, 110},
		cicontract.TimingTotal:             {100, 111},
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
	workloadIntervals := map[cicontract.TimingPhase][2]int64{
		cicontract.TimingStartup:  {103, 104},
		cicontract.TimingTestBody: {104, 110},
		cicontract.TimingTotal:    {103, 110},
	}
	for _, phase := range []cicontract.TimingPhase{cicontract.TimingStartup, cicontract.TimingTestBody, cicontract.TimingTotal} {
		interval := workloadIntervals[phase]
		observations = append(observations, measured(cicontract.TimingScopeWorkload, execution.ShardIdentity, execution.GateID, phase, interval[0], interval[1], NewTimingCacheEvidenceFromProfile(execution.ExecutionProfile)))
	}
	return observations
}

func TestTimingObservationMeasuredInterval(t *testing.T) {
	started := time.UnixMilli(100)
	observation := TimingObservation{JobID: "job-1", Scope: cicontract.TimingScopeRun, Phase: cicontract.TimingTotal, StartedAt: started, CompletedAt: started.Add(time.Millisecond), DurationMS: 1, Measurement: cicontract.ObservationMeasured, Aggregation: cicontract.TimingAggregationCriticalPath, CacheEvidence: NewNotApplicableCacheEvidence("run_has_no_workload_cache")}
	if err := observation.Validate(); err != nil {
		t.Fatal(err)
	}
}
