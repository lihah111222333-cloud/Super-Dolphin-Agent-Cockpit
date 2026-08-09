package gate

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExecutorPlanPendingExactGoResultsBindCanonicalProfilesAfterFailure(t *testing.T) {
	normalID := mustPendingGoTestWorkloadID(t, GateIDBackendTestWithGuard, "TestPendingNormal")
	raceID := mustPendingGoTestWorkloadID(t, GateIDBackendTestGuardWithRace, "TestPendingRace")
	request := executorPlanRequest{
		profile:    ProfileRelease,
		planDigest: "sha256:" + strings.Repeat("a", 64),
		gateIDs:    []GateID{GateIDAIMaintenanceSelfTest, GateID(normalID), GateID(raceID)},
		shard:      true,
	}
	failure := errors.New("preceding lane failed")
	report, err := executeGatePlanWithRunner(context.Background(), request, func(_ context.Context, lane int, id GateID) (PlanGateExecution, error) {
		if lane == 0 && id == GateIDAIMaintenanceSelfTest {
			result := successfulPlanGateResult(id)
			result.Status, result.ExitCode = ResultStatusFailed, 1
			return result, failure
		}
		t.Fatal("pending exact Go workload unexpectedly started")
		return PlanGateExecution{}, nil
	}, executorPlanTestNow)
	if !errors.Is(err, failure) {
		t.Fatalf("plan error = %v, want preceding lane failure", err)
	}
	assertPendingExactGoProfile(t, report, GateID(normalID), ResultStatusCancelled, CanonicalGoFlags(false))
	assertPendingExactGoProfile(t, report, GateID(raceID), ResultStatusCancelled, CanonicalGoFlags(true))
}

func TestExecutorPlanPendingExactGoResultBindsCanonicalRaceProfileAfterContextCancellation(t *testing.T) {
	raceID := mustPendingGoTestWorkloadID(t, GateIDBackendTestGuardWithRace, "TestPendingRaceDeadline")
	request := executorPlanRequest{
		profile:    ProfileRelease,
		planDigest: "sha256:" + strings.Repeat("b", 64),
		gateIDs:    []GateID{GateID(raceID)},
		shard:      true,
	}
	ctx, cancel := context.WithDeadline(context.Background(), executorPlanTestNow().Add(-time.Second))
	defer cancel()
	report, err := executeGatePlanWithRunner(ctx, request, func(context.Context, int, GateID) (PlanGateExecution, error) {
		t.Fatal("deadline-cancelled exact Go workload unexpectedly started")
		return PlanGateExecution{}, nil
	}, executorPlanTestNow)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("plan error = %v, want deadline cancellation", err)
	}
	assertPendingExactGoProfile(t, report, GateID(raceID), ResultStatusTimeout, CanonicalGoFlags(true))
}

func mustPendingGoTestWorkloadID(t *testing.T, parent GateID, name string) string {
	t.Helper()
	workload, err := NewGoTestWorkload(parent, "./internal/archtest", name, 1)
	if err != nil {
		t.Fatalf("NewGoTestWorkload(%q): %v", name, err)
	}
	return workload.ID
}

func assertPendingExactGoProfile(t *testing.T, report PlanExecutionReport, id GateID, wantStatus ResultStatus, wantFlags string) {
	t.Helper()
	for _, result := range report.Gates {
		if result.GateID != id {
			continue
		}
		if result.Status != wantStatus || result.ExitCode != -1 {
			t.Fatalf("pending result %q = status=%s exit=%d, want status=%s exit=-1", id, result.Status, result.ExitCode, wantStatus)
		}
		if result.ExecutionProfile.GoFlags != wantFlags {
			t.Fatalf("pending result %q GoFlags = %q, want %q", id, result.ExecutionProfile.GoFlags, wantFlags)
		}
		return
	}
	t.Fatalf("pending result %q not found in report %#v", id, report.Gates)
}
