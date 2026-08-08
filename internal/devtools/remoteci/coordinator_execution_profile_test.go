package remoteci

import (
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestAggregateWorkloadGateUsesBoundedDeterministicProofRoot(t *testing.T) {
	started := time.Date(2026, time.August, 6, 12, 0, 0, 0, time.UTC)
	workloads := largeAggregateWorkloadProofFixture(started, 1200)
	first := assertBoundedAggregateWorkloadProof(t, workloads)

	workloads[731].LogDigest = fmt.Sprintf("sha256:%064x", len(workloads)+99)
	tampered, _, err := aggregateWorkloadGate(gate.GateIDFrontendFullTest, workloads)
	if err != nil {
		t.Fatal(err)
	}
	if string(tampered.Log) == string(first.Log) || tampered.LogDigest == first.LogDigest {
		t.Fatal("changing one child proof did not change the parent proof root")
	}
}

// largeAggregateWorkloadProofFixture 构造足以复现旧无界父日志的确定性 workload 集合。
func largeAggregateWorkloadProofFixture(started time.Time, count int) []gate.PlanGateExecution {
	workloads := make([]gate.PlanGateExecution, count)
	for index := range workloads {
		workloads[index] = measuredAggregateWorkload(
			fmt.Sprintf("frontend:test-full::vitest-file::target-%04d", index),
			started.Add(time.Duration(index)*time.Microsecond),
			10,
			gate.ExecutionProfile{
				CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
				StartupMS: 2, TestBodyMS: 4, TotalMS: 10,
			},
		)
		workloads[index].ArgvDigest = fmt.Sprintf("sha256:%064x", index+1)
		workloads[index].LogDigest = fmt.Sprintf("sha256:%064x", index+2)
	}
	return workloads
}

// assertBoundedAggregateWorkloadProof 校验父证明固定有界且对同一输入可复现。
func assertBoundedAggregateWorkloadProof(t *testing.T, workloads []gate.PlanGateExecution) gate.PlanGateExecution {
	t.Helper()
	first, status, err := aggregateWorkloadGate(gate.GateIDFrontendFullTest, workloads)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := aggregateWorkloadGate(gate.GateIDFrontendFullTest, workloads)
	if err != nil {
		t.Fatal(err)
	}
	if status != gate.ResultStatusPassed || len(first.Log) >= 1024 || string(first.Log) != string(second.Log) || first.LogDigest != second.LogDigest {
		t.Fatalf("bounded aggregate proof = log_bytes=%d status=%q log=%q", len(first.Log), status, first.Log)
	}
	for _, want := range []string{"schema=1", fmt.Sprintf("workloads=%d", len(workloads)), fmt.Sprintf("passed=%d", len(workloads)), "failed=0", "proof_digest=sha256:"} {
		if !strings.Contains(string(first.Log), want) {
			t.Fatalf("bounded aggregate proof %q omits %q", first.Log, want)
		}
	}
	return first
}

func TestAggregateWorkloadGateUsesMeasuredIntervalUnionsAndCacheEvidence(t *testing.T) {
	started := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	workloads := []gate.PlanGateExecution{
		measuredAggregateWorkload("go-test:one", started, 10, gate.ExecutionProfile{
			CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationHit, CacheMeasurement: "measured",
			PrivateHitCount: 1, BaselineHitCount: 1, CachePutCount: 4,
			StartupMS: 2, TestBodyMS: 4, TotalMS: 10,
		}),
		measuredAggregateWorkload("go-test:two", started.Add(time.Millisecond), 12, gate.ExecutionProfile{
			CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationMiss, CacheMeasurement: "measured",
			BaselineHitCount: 2, CacheMissCount: 3,
			StartupMS: 3, TestBodyMS: 5, TotalMS: 12,
		}),
	}
	result, status, err := aggregateWorkloadGate(gate.GateIDBackendTestWithGuard, workloads)
	if err != nil {
		t.Fatal(err)
	}
	profile := result.ExecutionProfile
	assertAggregateWorkloadIdentity(t, result, status, started)
	assertAggregateWorkloadTiming(t, profile)
	assertAggregateWorkloadCacheEvidence(t, profile)
}

func TestAggregateWorkloadGateCanonicalizesNanosecondBoundaries(t *testing.T) {
	rawStarted := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC).Add(123456789 * time.Nanosecond)
	workloads := []gate.PlanGateExecution{
		{
			GateID: gate.GateID("go-test:nanosecond-one"), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: rawStarted, CompletedAt: rawStarted.Add(10*time.Millisecond + 700*time.Microsecond),
			ExecutionProfile: gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 2, TestBodyMS: 4, TotalMS: 10},
		},
		{
			GateID: gate.GateID("go-test:nanosecond-two"), Status: gate.ResultStatusPassed, ExitCode: 0,
			StartedAt: rawStarted.Add(time.Millisecond + 100*time.Microsecond), CompletedAt: rawStarted.Add(12*time.Millisecond + 900*time.Microsecond),
			ExecutionProfile: gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 3, TestBodyMS: 5, TotalMS: 11},
		},
	}
	result, status, err := aggregateWorkloadGate(gate.GateIDBackendTestWithGuard, workloads)
	if err != nil {
		t.Fatal(err)
	}
	wantStarted := rawStarted.UTC().Truncate(time.Millisecond)
	wantCompleted := rawStarted.Add(12*time.Millisecond + 900*time.Microsecond).UTC().Truncate(time.Millisecond)
	if status != gate.ResultStatusPassed || !result.StartedAt.Equal(wantStarted) || !result.CompletedAt.Equal(wantCompleted) || result.ExecutionProfile.TotalMS != 13 {
		t.Fatalf("aggregate timing = %#v status=%q, want [%s,%s] total=13", result, status, wantStarted, wantCompleted)
	}
}

func TestAggregateWorkloadGateAllowsMeasuredMixedCacheSources(t *testing.T) {
	started := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	workloads := []gate.PlanGateExecution{
		measuredAggregateWorkload("guard:none", started, 8, gate.ExecutionProfile{
			CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured",
			StartupMS: 2, TestBodyMS: 3, TotalMS: 8,
		}),
		measuredAggregateWorkload("go-test:cached", started.Add(time.Millisecond), 10, gate.ExecutionProfile{
			CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationHit, CacheMeasurement: "measured",
			BaselineHitCount: 4, CacheMissCount: 1, CachePutCount: 2,
			StartupMS: 2, TestBodyMS: 5, TotalMS: 10,
		}),
	}
	result, status, err := aggregateWorkloadGate(gate.GateIDBackendTestWithGuard, workloads)
	if err != nil {
		t.Fatal(err)
	}
	profile := result.ExecutionProfile
	if status != gate.ResultStatusPassed || profile.CacheSource != "go_build_cache" ||
		profile.CacheStatus != gate.CacheObservationHit || profile.BaselineHitCount != 4 ||
		profile.CacheMissCount != 1 || profile.CachePutCount != 2 {
		t.Fatalf("mixed aggregate profile = %#v status=%q", profile, status)
	}
}

func assertAggregateWorkloadIdentity(t *testing.T, result gate.PlanGateExecution, status gate.ResultStatus, started time.Time) {
	t.Helper()
	if status != gate.ResultStatusPassed || !result.StartedAt.Equal(started) || !result.CompletedAt.Equal(started.Add(13*time.Millisecond)) {
		t.Fatalf("aggregate identity/status = %#v status=%q", result, status)
	}
}

func assertAggregateWorkloadTiming(t *testing.T, profile gate.ExecutionProfile) {
	t.Helper()
	if profile.StartupMS != 4 || profile.TestBodyMS != 7 || profile.TotalMS != 13 {
		t.Fatalf("aggregate phase timing = %#v, want startup=4 body=7 total=13", profile)
	}
}

func assertAggregateWorkloadCacheEvidence(t *testing.T, profile gate.ExecutionProfile) {
	t.Helper()
	if profile.CacheMeasurement != "measured" || profile.CacheSource != "go_build_cache" || profile.CacheStatus != gate.CacheObservationHit ||
		profile.PrivateHitCount != 1 || profile.BaselineHitCount != 3 || profile.CacheMissCount != 3 || profile.CachePutCount != 4 {
		t.Fatalf("aggregate cache evidence = %#v", profile)
	}
}

func TestAggregateWorkloadGateRejectsUnprovableProfiles(t *testing.T) {
	started := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	valid := measuredAggregateWorkload("go-test:one", started, 10, gate.ExecutionProfile{
		CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationMiss, CacheMeasurement: "measured",
		CacheMissCount: 1, StartupMS: 2, TestBodyMS: 4, TotalMS: 10,
	})
	for _, test := range aggregateUnprovableProfileCases(started, valid) {
		t.Run(test.name, func(t *testing.T) {
			assertAggregateWorkloadGateError(t, test.workloads, test.want)
		})
	}
}

type aggregateProfileErrorCase struct {
	name      string
	workloads []gate.PlanGateExecution
	want      string
}

func aggregateUnprovableProfileCases(started time.Time, valid gate.PlanGateExecution) []aggregateProfileErrorCase {
	return []aggregateProfileErrorCase{
		{name: "missing profile", workloads: []gate.PlanGateExecution{{GateID: "go-test:missing", StartedAt: started, CompletedAt: started.Add(10 * time.Millisecond)}}, want: "cache source is invalid"},
		{name: "unmeasured profile", workloads: []gate.PlanGateExecution{withAggregateProfile(valid, func(profile *gate.ExecutionProfile) { profile.CacheMeasurement = "not_measured" })}, want: "cache measurement is invalid"},
		{name: "total drift", workloads: []gate.PlanGateExecution{withAggregateProfile(valid, func(profile *gate.ExecutionProfile) { profile.TotalMS++ })}, want: "does not match its interval"},
		{name: "duplicate workload", workloads: []gate.PlanGateExecution{valid, valid}, want: "repeats workload"},
		{name: "unbounded materialize phase", workloads: []gate.PlanGateExecution{withAggregateProfile(valid, func(profile *gate.ExecutionProfile) { profile.MaterializeMS = 1 })}, want: "phase durations without aggregate interval boundaries"},
		{name: "cache overflow", workloads: []gate.PlanGateExecution{
			withAggregateProfile(valid, func(profile *gate.ExecutionProfile) { profile.CacheMissCount = math.MaxUint64 }),
			measuredAggregateWorkload("go-test:two", started, 10, gate.ExecutionProfile{CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationMiss, CacheMeasurement: "measured", CacheMissCount: 1, StartupMS: 2, TestBodyMS: 4, TotalMS: 10}),
		}, want: "overflows uint64"},
	}
}

func assertAggregateWorkloadGateError(t *testing.T, workloads []gate.PlanGateExecution, want string) {
	t.Helper()
	if _, _, err := aggregateWorkloadGate(gate.GateIDBackendTestWithGuard, workloads); err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("aggregateWorkloadGate() error = %v, want %q", err, want)
	}
}

func measuredAggregateWorkload(id string, started time.Time, totalMS int64, profile gate.ExecutionProfile) gate.PlanGateExecution {
	return gate.PlanGateExecution{
		GateID: gate.GateID(id), Status: gate.ResultStatusPassed, ExitCode: 0,
		StartedAt: started, CompletedAt: started.Add(time.Duration(totalMS) * time.Millisecond),
		ExecutionProfile: profile,
	}
}

func withAggregateProfile(execution gate.PlanGateExecution, mutate func(*gate.ExecutionProfile)) gate.PlanGateExecution {
	mutate(&execution.ExecutionProfile)
	return execution
}
