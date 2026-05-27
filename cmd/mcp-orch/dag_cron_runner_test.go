package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
	orchcron "github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/cron"
	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

type fakeCronDaemon struct {
	started  chan struct{}
	stopped  chan struct{}
	startErr error
	stopErr  error
}

func newFakeCronDaemon() *fakeCronDaemon {
	return &fakeCronDaemon{started: make(chan struct{}), stopped: make(chan struct{})}
}

func (d *fakeCronDaemon) Start(context.Context) error {
	close(d.started)
	return d.startErr
}

func (d *fakeCronDaemon) Stop() error {
	close(d.stopped)
	return d.stopErr
}

func TestScheduledDAGCronRunnerRunsUntilContextCancelled(t *testing.T) {
	daemon := newFakeCronDaemon()
	runner := scheduledDAGCronRunner{daemon: daemon}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- runner.Run(ctx) }()
	waitForCronSignal(t, daemon.started, "start")
	cancel()
	waitForCronSignal(t, daemon.stopped, "stop")

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() error = %v, want nil", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run() did not return after context cancellation")
	}
}

func TestScheduledDAGCronRunnerReturnsStartError(t *testing.T) {
	startErr := errors.New("boom")
	daemon := newFakeCronDaemon()
	daemon.startErr = startErr
	runner := scheduledDAGCronRunner{daemon: daemon}

	err := runner.Run(context.Background())
	if !errors.Is(err, startErr) {
		t.Fatalf("Run() error = %v, want start error", err)
	}
	select {
	case <-daemon.stopped:
		t.Fatal("Stop() called after Start() failed")
	default:
	}
}

func TestProvideScheduledDAGCronRunnerConstructsRunner(t *testing.T) {
	runner, err := provideScheduledDAGCronRunner(
		stubDAGScheduleStore{},
		stubAdvisoryLocker{},
		stubCronOrchestrationService{},
		slog.New(slog.NewTextHandler(io.Discard, nil)),
	)
	if err != nil {
		t.Fatalf("provideScheduledDAGCronRunner() error = %v", err)
	}
	if _, ok := runner.(scheduledDAGCronRunner); !ok {
		t.Fatalf("runner type = %T, want scheduledDAGCronRunner", runner)
	}
}

func TestProvideScheduledDAGCronRunnerFailsFastOnNilDependencies(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	cases := []struct {
		name   string
		store  orchcron.DAGScheduleStore
		locker orchcron.AdvisoryLocker
		svc    contract.OrchestrationService
		logger *slog.Logger
	}{
		{name: "store", store: nil, locker: stubAdvisoryLocker{}, svc: stubCronOrchestrationService{}, logger: logger},
		{name: "locker", store: stubDAGScheduleStore{}, locker: nil, svc: stubCronOrchestrationService{}, logger: logger},
		{name: "service", store: stubDAGScheduleStore{}, locker: stubAdvisoryLocker{}, svc: nil, logger: logger},
		{name: "logger", store: stubDAGScheduleStore{}, locker: stubAdvisoryLocker{}, svc: stubCronOrchestrationService{}, logger: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := provideScheduledDAGCronRunner(tc.store, tc.locker, tc.svc, tc.logger); err == nil {
				t.Fatal("provideScheduledDAGCronRunner() error = nil, want fail-fast error")
			}
		})
	}
}

func TestScheduledDAGStarterMarksConsumedScheduledRun(t *testing.T) {
	starter := scheduledDAGStarter{svc: exhaustedCronOrchestrationService{}}

	err := starter.StartDAG(context.Background(), orchcron.ScheduledDAGStartRequest{DagKey: "dag-1"})
	if !errors.Is(err, orchestration.ErrIdempotencyKeyExhausted) {
		t.Fatalf("StartDAG() error = %v, want ErrIdempotencyKeyExhausted", err)
	}
	var marker interface {
		ScheduledRunAlreadyConsumed() bool
	}
	if !errors.As(err, &marker) || !marker.ScheduledRunAlreadyConsumed() {
		t.Fatalf("StartDAG() error = %T, want ScheduledRunAlreadyConsumed marker", err)
	}
}

type exhaustedCronOrchestrationService struct {
	contract.OrchestrationService
}

func (exhaustedCronOrchestrationService) StartDAG(context.Context, contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	return contract.StartDAGResponse{}, &orchestration.IdempotencyKeyExhaustedError{RunKey: "run-1", Status: "failed"}
}

type stubCronOrchestrationService struct {
	contract.OrchestrationService
}

func (stubCronOrchestrationService) StartDAG(context.Context, contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	return contract.StartDAGResponse{}, nil
}

func waitForCronSignal(t *testing.T, ch <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for cron daemon %s", name)
	}
}
