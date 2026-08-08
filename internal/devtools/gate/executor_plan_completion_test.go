package gate

import (
	"testing"
	"time"
)

func TestCanonicalExactGoTestProcessCompletionExcludesLongProcessTail(t *testing.T) {
	workload, err := NewGoTestWorkload(GateIDBackendTestWithGuard, "./internal/archtest", "TestBoundary", 1)
	if err != nil {
		t.Fatal(err)
	}
	started := executorPlanTestNow().Add(200 * time.Second)
	timing := &executorExecutionTiming{setupMS: 1, bodyMS: 169_000, totalMS: 169_001}
	timings := []GoTestTiming{{Name: "TestBoundary", Status: GoTestStatusPass, DurationMS: 1_000}}
	completed := canonicalExactGoTestProcessCompletion(GateID(workload.ID), timings, started, timing)
	wantCompleted := started.Add(time.Millisecond + time.Second)
	if !completed.Equal(wantCompleted) {
		t.Fatalf("canonical exact completion = %v, want %v", completed, wantCompleted)
	}
	if timing.setupMS != 1 || timing.bodyMS != 1_000 || timing.totalMS != 1_001 {
		t.Fatalf("canonical exact timing = %#v, want setup=1ms body=1s", timing)
	}
	profile, err := executionProfileForGate(GateID(workload.ID), ExecutorProgram{}, timings, started, completed, timing)
	if err != nil {
		t.Fatal(err)
	}
	if profile.StartupMS != 1 || profile.TestBodyMS != 1_000 || profile.TotalMS != 1_001 {
		t.Fatalf("profile = %#v, want startup=1ms body=1s total=1001ms", profile)
	}
}
