package waitaggregate

import (
	"context"
	"errors"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/accesswait"
)

// TestWaitAggregateWorkerPanicIsIndeterminate proves that the worker owner,
// rather than the generic SafeGo recovery boundary, publishes the failure.
func TestWaitAggregateWorkerPanicIsIndeterminate(t *testing.T) {
	t.Parallel()
	const secret = "waiter panic secret"
	request := contractRequest(time.Now().UTC().Add(time.Second), ConditionAny, target("one", "panic"))
	waiter := panicWaiter{value: secret}

	started := time.Now()
	result, err := waitAggregate(context.Background(), waiter, request)
	if err != nil {
		t.Fatalf("Wait() error = %v, want nil with indeterminate item", err)
	}
	if result.Status != StatusIndeterminate || result.Items[0].Outcome != OutcomeIndeterminate {
		t.Fatalf("result = %#v, want indeterminate result/item", result)
	}
	if !errors.Is(result.Items[0].Err, ErrWaiterFailure) {
		t.Fatalf("item error = %v, want ErrWaiterFailure", result.Items[0].Err)
	}
	if strings.Contains(result.Items[0].Err.Error(), secret) {
		t.Fatalf("item error leaked panic detail: %v", result.Items[0].Err)
	}
	if elapsed := time.Since(started); elapsed >= 500*time.Millisecond {
		t.Fatalf("panic worker waited for deadline: elapsed=%v", elapsed)
	}
}

// TestWaitAggregateWorkerGoexitIsIndeterminate covers an owner that exits
// without returning values even though no panic value is available to recover.
func TestWaitAggregateWorkerGoexitIsIndeterminate(t *testing.T) {
	t.Parallel()
	request := contractRequest(time.Now().UTC().Add(time.Second), ConditionAny, target("one", "goexit"))
	result, err := waitAggregate(context.Background(), goexitWaiter{}, request)
	if err != nil {
		t.Fatalf("Wait() error = %v, want nil with indeterminate item", err)
	}
	if result.Status != StatusIndeterminate || result.Items[0].Outcome != OutcomeIndeterminate {
		t.Fatalf("result = %#v, want indeterminate result/item", result)
	}
	if !errors.Is(result.Items[0].Err, ErrWaiterFailure) {
		t.Fatalf("item error = %v, want ErrWaiterFailure", result.Items[0].Err)
	}
}

// TestWaitAggregateWorkerFailureCancelsSibling verifies that owner failure
// does not leave a context-aware sibling waiting until the aggregate deadline.
func TestWaitAggregateWorkerFailureCancelsSibling(t *testing.T) {
	t.Parallel()
	request := contractRequest(time.Now().UTC().Add(time.Second), ConditionAll, target("panic", "panic"), target("sibling", "sibling"))
	waiter := &failureSiblingWaiter{cancelled: make(chan struct{})}
	result, err := waitAggregate(context.Background(), waiter, request)
	if err != nil {
		t.Fatalf("Wait() error = %v, want nil with indeterminate item", err)
	}
	if result.Status != StatusIndeterminate {
		t.Fatalf("result = %#v, want indeterminate", result)
	}
	select {
	case <-waiter.cancelled:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("sibling waiter did not observe owner-failure cancellation")
	}
}

type panicWaiter struct{ value any }

func (w panicWaiter) Wait(context.Context, accesswait.PollRequest) (accesswait.WaitResult, error) {
	panic(w.value) // archguard:ignore panic_count -- test injects a waiter panic to prove stable indeterminate propagation.
}

type goexitWaiter struct{}

func (goexitWaiter) Wait(context.Context, accesswait.PollRequest) (accesswait.WaitResult, error) {
	runtime.Goexit()
	return accesswait.WaitResult{}, nil
}

type failureSiblingWaiter struct{ cancelled chan struct{} }

func (w *failureSiblingWaiter) Wait(ctx context.Context, poll accesswait.PollRequest) (accesswait.WaitResult, error) {
	if poll.WaitHandleID == "panic" {
		panic("failure sibling secret") // archguard:ignore panic_count -- test injects an owner failure to prove sibling cancellation.
	}
	<-ctx.Done()
	select {
	case <-w.cancelled:
	default:
		close(w.cancelled)
	}
	return accesswait.WaitResult{}, ctx.Err()
}
