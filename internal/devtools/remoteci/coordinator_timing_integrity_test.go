package remoteci

import (
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

// TestRemoteWorkloadProjectionRejectsExactTimingMismatch 固定 coordinator 不得把矛盾 timing 投影进 workload 结果。
func TestRemoteWorkloadProjectionRejectsExactTimingMismatch(t *testing.T) {
	workload, err := gate.NewGoTestWorkload(gate.GateIDBackendTestWithGuard, "./internal/archtest", "TestCompileGroupSelector000", 1)
	if err != nil {
		t.Fatal(err)
	}
	start := time.UnixMilli(1_700_000_000_000).UTC()
	execution := gate.PlanGateExecution{
		GateID: gate.GateID(workload.ID), Status: gate.ResultStatusFailed, ExitCode: 1,
		StartedAt: start, CompletedAt: start.Add(11_521 * time.Millisecond),
		TestTimings:      []gate.GoTestTiming{{Name: "TestCompileGroupSelector000", Status: gate.GoTestStatusFail, DurationMS: 229_952}},
		ExecutionProfile: gate.ExecutionProfile{CacheSource: "none", CacheStatus: gate.CacheObservationNotApplicable, CacheMeasurement: "measured", StartupMS: 1, TestBodyMS: 11_520, TotalMS: 11_521},
	}
	catalog := gate.WorkloadCatalog{Workloads: []gate.Workload{workload}}
	_, err = remoteWorkloadExecutions(catalog, map[string]gate.PlanGateExecution{workload.ID: execution})
	if err == nil || !strings.Contains(err.Error(), "timing evidence") || !strings.Contains(err.Error(), "exceeds measured total interval") {
		t.Fatalf("mismatched workload projection error = %v, want fail-fast timing evidence", err)
	}
}
