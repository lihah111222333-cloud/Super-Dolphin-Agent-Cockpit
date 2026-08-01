package remoteci

import (
	"context"
	"errors"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/devtools/gate"
)

func TestCoordinatorRunRetriesOnlyPreviouslyFailedWorkloads(t *testing.T) {
	repository, input := remoteRunFixture(t)
	input.RepositoryRoot = repository
	store := &coordinatorStore{}
	first, err := newTestCoordinator(
		t,
		store,
		&coordinatorRuntime{failReport: true, failureLog: "injected failure\n"},
	).Run(context.Background(), input)
	if !errors.Is(err, ErrGateFailed) {
		t.Fatalf("first Run() error = %v", err)
	}
	failedWorkloads, totalWorkloads := failedCoordinatorWorkloads(t, first)
	retry, err := newTestCoordinator(t, store, &coordinatorRuntime{}).Run(context.Background(), input)
	if err != nil {
		t.Fatalf("retry Run() error = %v", err)
	}
	executedWorkloads, executed := executedCoordinatorWorkloads(retry)
	assertCoordinatorRetryMatchesFailures(t, failedWorkloads, executedWorkloads, executed, totalWorkloads)
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

func assertCoordinatorRetryMatchesFailures(
	t *testing.T,
	failed map[gate.GateID]struct{},
	executed map[gate.GateID]struct{},
	executedCount int,
	total int,
) {
	t.Helper()
	if executedCount != len(failed) || len(executed) != len(failed) || executedCount >= total {
		t.Fatalf("retry executed=%d unique=%d want failed=%d total=%d", executedCount, len(executed), len(failed), total)
	}
	for workloadID := range failed {
		if _, ok := executed[workloadID]; !ok {
			t.Fatalf("retry omitted failed workload %q", workloadID)
		}
	}
	for workloadID := range executed {
		if _, ok := failed[workloadID]; !ok {
			t.Fatalf("retry executed successful workload %q", workloadID)
		}
	}
}
