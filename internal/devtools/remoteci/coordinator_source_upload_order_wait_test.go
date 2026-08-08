package remoteci

import (
	"context"
	"errors"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

type sourceUploadOrderWaitResult struct {
	outcome sourceUploadOrderOutcome
	err     error
}

// startSourceUploadOrderWait 通过 errgroup 启动等待器，避免测试中的裸 goroutine。
func startSourceUploadOrderWait(
	group *errgroup.Group,
	ctx context.Context,
	outcomes <-chan sourceUploadOrderOutcome,
	hooks sourceUploadOrderRunWaitHooks,
	onSlow func(time.Duration),
) <-chan sourceUploadOrderWaitResult {
	results := make(chan sourceUploadOrderWaitResult, 1)
	group.Go(func() error {
		outcome, err := waitSourceUploadOrderOutcome(ctx, outcomes, hooks, onSlow)
		results <- sourceUploadOrderWaitResult{outcome: outcome, err: err}
		return nil
	})
	return results
}

// awaitSourceUploadOrderWait 等待异步 seam 结果并回收 errgroup。
func awaitSourceUploadOrderWait(t *testing.T, group *errgroup.Group, results <-chan sourceUploadOrderWaitResult) sourceUploadOrderWaitResult {
	t.Helper()
	result := <-results
	if err := group.Wait(); err != nil {
		t.Fatalf("source upload order wait group error = %v", err)
	}
	return result
}

func TestWaitSourceUploadOrderOutcomeAllowsCompletionAfterSlowWarning(t *testing.T) {
	start := time.Date(2026, 8, 7, 0, 0, 0, 0, time.UTC)
	warning := make(chan time.Time)
	outcomes := make(chan sourceUploadOrderOutcome, 1)
	var warned time.Duration
	nowCalls := 0
	var timerDuration time.Duration
	var waitGroup errgroup.Group
	resultCh := startSourceUploadOrderWait(
		&waitGroup,
		context.Background(), outcomes,
		sourceUploadOrderRunWaitHooks{
			now: func() time.Time {
				nowCalls++
				if nowCalls == 1 {
					return start
				}
				return start.Add(5 * time.Second)
			},
			newTimer: func(duration time.Duration) (<-chan time.Time, func()) {
				timerDuration = duration
				return warning, func() {}
			},
		},
		func(elapsed time.Duration) { warned = elapsed },
	)
	t.Cleanup(func() { _ = waitGroup.Wait() })
	warning <- start.Add(5 * time.Second)
	outcomes <- sourceUploadOrderOutcome{result: RunResult{JobID: "slow-success"}}

	result := awaitSourceUploadOrderWait(t, &waitGroup, resultCh)
	if result.err != nil {
		t.Fatalf("waitSourceUploadOrderOutcome() error = %v", result.err)
	}
	if result.outcome.result.JobID != "slow-success" {
		t.Fatalf("outcome = %#v, want slow success", result.outcome)
	}
	if warned != 5*time.Second {
		t.Fatalf("slow warning elapsed = %s, want 5s", warned)
	}
	if got, want := nowCalls, 2; got != want {
		t.Fatalf("wait clock calls = %d, want %d", got, want)
	}
	if timerDuration != sourceUploadOrderSlowWarningAfter {
		t.Fatalf("warning timer duration = %s, want %s", timerDuration, sourceUploadOrderSlowWarningAfter)
	}
}

func TestWaitSourceUploadOrderOutcomeWarningDoesNotCancel(t *testing.T) {
	start := time.Date(2026, 8, 7, 1, 0, 0, 0, time.UTC)
	warning := make(chan time.Time)
	outcomes := make(chan sourceUploadOrderOutcome, 1)
	var warned time.Duration
	nowCalls := 0
	var timerDuration time.Duration
	var waitGroup errgroup.Group
	resultCh := startSourceUploadOrderWait(
		&waitGroup,
		context.Background(), outcomes,
		sourceUploadOrderRunWaitHooks{
			now: func() time.Time {
				nowCalls++
				if nowCalls == 1 {
					return start
				}
				return start.Add(sourceUploadOrderSlowWarningAfter)
			},
			newTimer: func(duration time.Duration) (<-chan time.Time, func()) {
				timerDuration = duration
				return warning, func() {}
			},
		},
		func(elapsed time.Duration) { warned = elapsed },
	)
	t.Cleanup(func() { _ = waitGroup.Wait() })
	warning <- start.Add(sourceUploadOrderSlowWarningAfter)
	outcomes <- sourceUploadOrderOutcome{result: RunResult{JobID: "warning-success"}}

	result := awaitSourceUploadOrderWait(t, &waitGroup, resultCh)
	if result.err != nil {
		t.Fatalf("waitSourceUploadOrderOutcome() error = %v", result.err)
	}
	if result.outcome.result.JobID != "warning-success" {
		t.Fatalf("outcome = %#v, want success after warning", result.outcome)
	}
	if warned != sourceUploadOrderSlowWarningAfter {
		t.Fatalf("warning elapsed = %s, want %s", warned, sourceUploadOrderSlowWarningAfter)
	}
	if timerDuration != sourceUploadOrderSlowWarningAfter {
		t.Fatalf("warning timer duration = %s, want %s", timerDuration, sourceUploadOrderSlowWarningAfter)
	}
}

func TestWaitSourceUploadOrderOutcomeFailsOnlyAtContextDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	warning := make(chan time.Time)
	var waitGroup errgroup.Group
	resultCh := startSourceUploadOrderWait(
		&waitGroup,
		ctx,
		make(chan sourceUploadOrderOutcome),
		sourceUploadOrderRunWaitHooks{
			now: time.Now,
			newTimer: func(time.Duration) (<-chan time.Time, func()) {
				return warning, func() {}
			},
		},
		nil,
	)
	t.Cleanup(func() { _ = waitGroup.Wait() })
	cancel()
	result := awaitSourceUploadOrderWait(t, &waitGroup, resultCh)
	err := result.err
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("waitSourceUploadOrderOutcome() error = %v, want context cancellation", err)
	}
}
