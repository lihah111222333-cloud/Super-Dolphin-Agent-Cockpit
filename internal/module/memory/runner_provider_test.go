package memory

import (
	"context"
	"testing"
	"time"
)

func TestAutoDreamSchedulerAsRunnerRun(t *testing.T) {
	t.Parallel()
	runner := autoDreamSchedulerAsRunner(newAutoDreamScheduler(nil, nil))
	assertMemoryRunnerStopsAfterCancel(t, runner.Run)
}

func TestNestedIngestWorkerAsRunnerRun(t *testing.T) {
	t.Parallel()
	runner := nestedIngestWorkerAsRunner(newNestedIngestWorker(nil, nil))
	assertMemoryRunnerStopsAfterCancel(t, runner.Run)
}

func TestTeamSyncCoordinatorAsRunnerRun(t *testing.T) {
	t.Parallel()
	runner := teamSyncCoordinatorAsRunner(newTeamSyncCoordinator(nil, nil, nil))
	assertMemoryRunnerStopsAfterCancel(t, runner.Run)
}

func assertMemoryRunnerStopsAfterCancel(t *testing.T, run func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()
	time.Sleep(10 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after context cancel")
	}
}
