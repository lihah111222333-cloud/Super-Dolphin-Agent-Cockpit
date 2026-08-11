package remoteci

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCoordinatorRunReexecutesOnlyFailedWorkloadsAfterPreviousFailure(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	firstCoordinator := newTestCoordinator(
		t,
		store,
		&coordinatorRuntime{failReport: true, failureLog: "injected failure\n"},
	)
	first, err := runCoordinatorTest(t, firstCoordinator, context.Background(), input)
	if !errors.Is(err, ErrGateFailed) {
		t.Fatalf("first Run() error = %v", err)
	}
	failedWorkloads, totalWorkloads := failedCoordinatorWorkloads(t, first)
	retryCoordinator := newTestCoordinator(t, store, &coordinatorRuntime{})
	retryCoordinator.newID = func() (string, error) { return "job-0123456789abcdef01234568", nil }
	retry, err := runCoordinatorTest(t, retryCoordinator, context.Background(), input)
	if err != nil {
		t.Fatalf("retry Run() error = %v", err)
	}
	if retry.JobID == first.JobID {
		t.Fatalf("retry reused self-origin job ID %q", retry.JobID)
	}
	executedWorkloads, executed := executedCoordinatorWorkloads(retry)
	assertCoordinatorRetryReexecutesOnlyFailures(t, retry, failedWorkloads, executedWorkloads, executed, totalWorkloads)
}

func failedCoordinatorWorkloads(t *testing.T, result RunResult) (map[gate.GateID]struct{}, int) {
	t.Helper()
	total := 0
	failed := make(map[gate.GateID]struct{})
	for _, shard := range result.Shards {
		total += len(shard.Report.Gates)
		for _, execution := range shard.Report.Gates {
			if execution.Status == gate.ResultStatusFailed {
				failed[execution.GateID] = struct{}{}
			}
		}
	}
	if len(failed) == 0 || len(failed) >= total {
		t.Fatalf("first Run() failed=%d total=%d, want a non-empty strict subset", len(failed), total)
	}
	return failed, total
}

func executedCoordinatorWorkloads(result RunResult) (map[gate.GateID]struct{}, int) {
	executed := 0
	workloads := make(map[gate.GateID]struct{})
	for _, shard := range result.Shards {
		executed += len(shard.ExecutedWorkloads)
		for _, workloadID := range shard.ExecutedWorkloads {
			workloads[workloadID] = struct{}{}
		}
	}
	return workloads, executed
}

func assertCoordinatorRetryReexecutesOnlyFailures(t *testing.T, result RunResult, failed, executed map[gate.GateID]struct{}, executedCount, total int) {
	t.Helper()
	if executedCount != len(failed) || len(executed) != len(failed) {
		t.Fatalf("retry executed=%d unique=%d, want failed workload count=%d", executedCount, len(executed), len(failed))
	}
	for workloadID := range failed {
		if _, ok := executed[workloadID]; !ok {
			t.Fatalf("failed workload %q was not re-executed", workloadID)
		}
	}
	if len(result.ReusedWorkloads) != total-len(failed) || len(result.CacheMissWorkloads) != len(failed) {
		t.Fatalf("retry reuse/miss counts = reused=%d misses=%d, want reused=%d misses=%d", len(result.ReusedWorkloads), len(result.CacheMissWorkloads), total-len(failed), len(failed))
	}
	assertCoordinatorShardsContainOnlyMisses(t, result)
}
