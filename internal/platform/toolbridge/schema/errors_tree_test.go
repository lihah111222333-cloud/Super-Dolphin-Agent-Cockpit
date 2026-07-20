package schema

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

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
