package schema

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/safego"
)

type delayedCyclicError struct {
	delay time.Duration
}

func (err *delayedCyclicError) Error() string {
	return "cyclic fixture"
}

func (err *delayedCyclicError) Unwrap() error {
	time.Sleep(err.delay)
	return err
}

type nonComparableUnwrapError struct {
	children []error
}

func (err nonComparableUnwrapError) Error() string {
	return "non-comparable fixture"
}

func (err nonComparableUnwrapError) Unwrap() []error {
	return err.children
}

func TestHelperLimiterReturnsSlotAfterNestedLateReap(t *testing.T) {
	limiter := newHelperLimiter(1)
	var completeLateReap func()
	nested := fmt.Errorf("cleanup failed: %w", errors.Join(
		errors.New("cleanup context"),
		newDiagnostic(CodeReapFailed, "fixture did not reap", nil),
	))
	operationErr := errors.Join(
		newDiagnostic(CodeTimeout, "fixture timed out", context.DeadlineExceeded),
		nested,
	)
	if ErrorCode(operationErr) != CodeTimeout {
		t.Fatalf("joined operation code = %q, want primary %q", ErrorCode(operationErr), CodeTimeout)
	}

	if _, err := limiter.run(context.Background(), func(capacity *helperCapacityTracker) (Result, error) {
		completeLateReap = capacity.registerLateReap()
		return Result{}, operationErr
	}); !errors.Is(err, operationErr) {
		t.Fatalf("limiter.run() error = %v, want joined operation error", err)
	}

	started := false
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started {
		t.Fatal("operation started after a nested reap failure consumed capacity")
	}
	if ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("capacity code = %q, want %q; error=%v", ErrorCode(err), CodeCapacityExhausted, err)
	}
	if completeLateReap == nil {
		t.Fatal("late reap release callback was not provided")
	}
	completeLateReap()
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, nil
	}); err != nil {
		t.Fatalf("limiter.run() after nested late reap error = %v", err)
	}
}

func TestHelperLimiterKeepsSlotForMixedManagedAndUnmanagedReapFailures(t *testing.T) {
	limiter := newHelperLimiter(1)
	var completeManagedReap func()
	managed := newDiagnostic(CodeReapFailed, "managed fixture did not reap", nil)
	unmanaged := newDiagnostic(CodeReapFailed, "unmanaged fixture did not reap", nil)

	if _, err := limiter.run(context.Background(), func(capacity *helperCapacityTracker) (Result, error) {
		completeManagedReap = capacity.registerLateReap()
		return Result{}, errors.Join(managed, unmanaged)
	}); !errors.Is(err, managed) || !errors.Is(err, unmanaged) {
		t.Fatalf("limiter.run() error = %v, want both reap failures", err)
	}
	completeManagedReap()

	started := false
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started || ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("mixed reap failures released capacity: started=%v code=%q error=%v", started, ErrorCode(err), err)
	}
}

func TestHelperLimiterCyclicErrorReturnsAndKeepsSlot(t *testing.T) {
	limiter := newHelperLimiter(1)
	cyclic := &delayedCyclicError{delay: time.Millisecond}
	result := make(chan error, 1)
	safego.Go(context.Background(), nil, "toolbridge.schema-cyclic-error-test", func(context.Context) {
		_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
			return Result{}, cyclic
		})
		result <- err
	})

	select {
	case err := <-result:
		if err != cyclic {
			t.Fatalf("limiter.run() error = %v, want original cyclic error", err)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("limiter.run() hung while traversing a cyclic error")
	}
	assertHelperLimiterFailClosed(t, limiter, "cyclic error")
}

func TestHelperLimiterNonComparableErrorKeepsSlotWithoutPanic(t *testing.T) {
	limiter := newHelperLimiter(1)
	fixture := nonComparableUnwrapError{children: []error{
		newDiagnostic(CodeReapFailed, "unmanaged fixture did not reap", nil),
	}}
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, fixture
	}); err == nil || err.Error() != fixture.Error() {
		t.Fatalf("limiter.run() error = %v, want original non-comparable error", err)
	}
	assertHelperLimiterFailClosed(t, limiter, "non-comparable error")
}

func TestHelperLimiterDeepErrorTreeReturnsAndKeepsSlot(t *testing.T) {
	limiter := newHelperLimiter(1)
	var fixture error = errors.New("deep fixture")
	for range 128 {
		fixture = fmt.Errorf("wrapped: %w", fixture)
	}
	if _, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		return Result{}, fixture
	}); err != fixture {
		t.Fatalf("limiter.run() error = %v, want original deep error", err)
	}
	assertHelperLimiterFailClosed(t, limiter, "deep error tree")
}

func assertHelperLimiterFailClosed(t *testing.T, limiter *helperLimiter, stage string) {
	t.Helper()
	started := false
	_, err := limiter.run(context.Background(), func(*helperCapacityTracker) (Result, error) {
		started = true
		return Result{}, nil
	})
	if started || ErrorCode(err) != CodeCapacityExhausted {
		t.Fatalf("%s released capacity: started=%v code=%q error=%v", stage, started, ErrorCode(err), err)
	}
}
