package gate

import (
	"testing"
	"time"
)

func TestExecutionProfileUsesMeasuredGoCommandBodyAndChecksExactTestEvidence(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	started := executorPlanTestNow()
	completed := started.Add(1500 * time.Millisecond)
	timing := &executorExecutionTiming{setupMS: 500, bodyMS: 1_000, totalMS: 1_500}
	timings := []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 400}, {Name: "TestBoundary/subcase", Status: GoTestStatusPass, DurationMS: 900}}
	profile, err := executionProfileForGate(GateID(workload.ID), ExecutorProgram{}, timings, started, completed, timing)
	if err != nil || profile.TestBodyMS != 1_000 || profile.StartupMS != 500 {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	if _, err := executionProfileForGate(GateID(workload.ID), ExecutorProgram{}, []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 400}, {Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 401}}, started, completed, timing); err == nil {
		t.Fatal("duplicate top-level timing was accepted")
	}
	if _, err := executionProfileForGate(GateID(workload.ID), ExecutorProgram{}, []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 1001}}, started, completed, timing); err == nil {
		t.Fatal("overlong top-level timing was accepted")
	}
}

func TestExecutionProfileRecordsMeasuredBodyForGoPackageWorkload(t *testing.T) {
	workload, err := NewGoPackageWorkload(GateIDBackendTestWithGuard, "./internal/example", 1)
	if err != nil {
		t.Fatal(err)
	}
	started := executorPlanTestNow()
	profile, err := executionProfileForGate(
		GateID(workload.ID), ExecutorProgram{}, nil, started, started.Add(1_500*time.Millisecond),
		&executorExecutionTiming{setupMS: 500, bodyMS: 1_000, totalMS: 1_500},
	)
	if err != nil || profile.TestBodyMS != 1_000 || profile.StartupMS != 500 {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
}

func TestExecutorTimingPreservesPositiveSubMillisecondPhases(t *testing.T) {
	started := executorPlanTestNow()
	bodyStarted := started.Add(400 * time.Microsecond)
	completed := started.Add(time.Millisecond)
	timing := &executorExecutionTiming{}
	recordExecutorExecutionTiming(timing, started, bodyStarted, completed)
	if timing.setupMS != 1 || timing.bodyMS != 1 || timing.totalMS != 2 {
		t.Fatalf("timing = %#v, want setup=1 body=1 total=2", timing)
	}
	profile, err := executionProfileForGate("guard:sub-millisecond", ExecutorProgram{}, nil, started, completed, timing)
	if err != nil {
		t.Fatalf("executionProfileForGate() error = %v", err)
	}
	if profile.StartupMS != 1 || profile.TestBodyMS != 1 || profile.TotalMS != 2 {
		t.Fatalf("profile = %#v, want startup=1 body=1 total=2", profile)
	}
	normalizedCompleted := normalizedExecutionCompletedAt(started, completed, profile)
	if got := normalizedCompleted.Sub(started); got != 2*time.Millisecond {
		t.Fatalf("normalized total interval = %v, want 2ms", got)
	}
}

func TestFailedStartupProfileKeepsBodyNotStarted(t *testing.T) {
	started := executorPlanTestNow()
	profile := measuredFailedStartupExecutionProfile(started, started.Add(400*time.Microsecond))
	if profile.StartupMS != 1 || profile.TestBodyMS != 0 || profile.TotalMS != 1 {
		t.Fatalf("profile = %#v, want measured 1ms startup and unstarted body", profile)
	}
	if err := profile.Validate(); err != nil {
		t.Fatalf("failed startup profile validation: %v", err)
	}
}

func TestInvalidExactGoEventPreservesMeasuredStartupAndBody(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/example", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	started := executorPlanTestNow()
	profile, err := executionProfileOrFailedStartup(
		GateID(workload.ID),
		ExecutorProgram{},
		nil,
		started,
		started.Add(1500*time.Millisecond),
		&executorExecutionTiming{setupMS: 500, bodyMS: 1000, totalMS: 1500},
	)
	if err == nil {
		t.Fatal("missing exact Go event was accepted")
	}
	if profile.StartupMS != 500 || profile.TestBodyMS != 1000 || profile.TotalMS != 1500 {
		t.Fatalf("profile = %#v, want preserved measured phases", profile)
	}
}

func TestFrontendExecutionProfileDoesNotInferNPMCacheHit(t *testing.T) {
	started := executorPlanTestNow()
	completed := started.Add(1500 * time.Millisecond)
	profile, err := executionProfileForGate(
		GateIDFrontendLint,
		ExecutorProgram{NeedsFrontendSeed: true},
		nil,
		started,
		completed,
		&executorExecutionTiming{setupMS: 500, bodyMS: 1000, totalMS: 1500, viteCacheSeedHit: true},
	)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Frontend == nil || profile.Frontend.NPMCacheHit ||
		profile.Frontend.NPMCacheNotApplicableReason != "npm_cache_lookup_not_observed" {
		t.Fatalf("frontend npm cache evidence = %#v", profile.Frontend)
	}
	if !profile.Frontend.ViteCacheHit {
		t.Fatalf("frontend Vite cache evidence = %#v", profile.Frontend)
	}
}
