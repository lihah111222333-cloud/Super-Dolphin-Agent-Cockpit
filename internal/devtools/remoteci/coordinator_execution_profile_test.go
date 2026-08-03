package remoteci

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestAggregateWorkloadGateUsesMeasuredIntervalUnionsAndCacheEvidence(t *testing.T) {
	started := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	workloads := []gate.PlanGateExecution{
		measuredAggregateWorkload("go-test:one", started, 10, gate.ExecutionProfile{
			CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationHit, CacheMeasurement: "measured",
			PrivateHitCount: 1, BaselineHitCount: 1, BaselineHitByGeneration: map[string]uint64{"00000000000000000007": 1}, CachePutCount: 4,
			StartupMS: 2, TestBodyMS: 4, TotalMS: 10,
		}),
		measuredAggregateWorkload("go-test:two", started.Add(time.Millisecond), 12, gate.ExecutionProfile{
			CacheSource: "go_build_cache", CacheStatus: gate.CacheObservationMiss, CacheMeasurement: "measured",
			BaselineHitCount: 2, BaselineHitByGeneration: map[string]uint64{"00000000000000000007": 2}, CacheMissCount: 3,
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
		profile.PrivateHitCount != 1 || profile.BaselineHitCount != 3 || profile.CacheMissCount != 3 || profile.CachePutCount != 4 ||
		profile.BaselineHitByGeneration["00000000000000000007"] != 3 {
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
		{name: "mixed cache source", workloads: []gate.PlanGateExecution{valid, withAggregateProfile(measuredAggregateWorkload("go-test:two", started, 10, gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 2, TestBodyMS: 4, TotalMS: 10}), func(*gate.ExecutionProfile) {})}, want: "measurement/source is inconsistent"},
		{name: "duplicate workload", workloads: []gate.PlanGateExecution{valid, valid}, want: "repeats workload"},
		{name: "unbounded materialize phase", workloads: []gate.PlanGateExecution{withAggregateProfile(valid, func(profile *gate.ExecutionProfile) { profile.MaterializeMS = 1 })}, want: "phase durations without aggregate interval boundaries"},
		{name: "baseline generation overflow", workloads: []gate.PlanGateExecution{withAggregateProfile(valid, func(profile *gate.ExecutionProfile) {
			profile.CacheStatus = gate.CacheObservationHit
			profile.CacheMissCount = 0
			profile.BaselineHitCount = 0
			profile.BaselineHitByGeneration = map[string]uint64{"00000000000000000007": math.MaxUint64, "00000000000000000008": 1}
		})}, want: "baseline generation counts overflow"},
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
